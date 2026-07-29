#! /bin/bash
set -euo pipefail

# Defaults to the devcontainer's mount point. Override for local (non-devcontainer)
# runs, e.g.: WORKSPACES_DIR="$HOME/.temporal-workshop-local" _scripts/start.sh
WORKSPACES_DIR="${WORKSPACES_DIR:-/workspaces}"

# The two dashboards this repo authors are loaded from the local checkout
# via --set-file, not fetched from a GitHub URL (see grafana-values.yaml) --
# this must be passed on every install/upgrade of Grafana, or a reload would
# silently keep whatever content was baked in at the last install instead of
# picking up local edits.
GRAFANA_DASHBOARD_FILES=(
  --set-file dashboards.default.bitovi-temporal-dashboard.json=grafana/bitovi_temporal_dashboard.json
  --set-file dashboards.default.benchmark-workshop-dashboard.json=grafana/benchmark_workshop_dashboard.json
)

# Number of dashboards Grafana should have provisioned once healthy: the 2
# local ones above plus the 5 GitHub-sourced ones in grafana/grafana-values.yaml.
EXPECTED_GRAFANA_DASHBOARD_COUNT=7

#######################################
# ensure_* functions: each checks whether its release/resource already
# exists and installs or upgrades accordingly, so the script behaves the
# same whether the k3d cluster is brand new or already running.
#######################################

ensure_k3d_cluster() {
  if k3d cluster list | grep -q '^dev\s'; then
    echo "k3d cluster 'dev' already exists, starting it..."
    k3d cluster start dev
  else
    echo "Creating k3d cluster 'dev'..."
    # Map host ports -> cluster nodeports
    k3d cluster create dev \
      -p "8080:30080@server:0" \
      -p "7233:30233@server:0" \
      -p "3000:30000@server:0" \
      -p "8233:31233@server:0" \
      -p "8181:31080@server:0" \
      -p "9090:30090@server:0" \
      -p "8090:30890@server:0" \
      --wait --timeout 120s
  fi

  mkdir -p "$WORKSPACES_DIR/.kube"
  k3d kubeconfig get dev > "$WORKSPACES_DIR/.kube/dev.yaml"
  chmod 666 "$WORKSPACES_DIR/.kube/dev.yaml"
}

ensure_postgres() {
  if helm status postgresql >/dev/null 2>&1; then
    # No --wait: verify_postgres owns confirming readiness (with its own
    # retry) so an unhealthy existing release can't hang here and take the
    # whole script down with it via `set -e`.
    echo "PostgreSQL release already exists, upgrading..."
    helm upgrade postgresql bitnami/postgresql --version 18.1.2 \
        -f helm/postgres-values.yaml \
        --reuse-values
  else
    echo "Installing PostgreSQL via Helm chart..."
    helm install postgresql bitnami/postgresql --version 18.1.2 \
        -f helm/postgres-values.yaml \
        --wait --timeout 5m
  fi
}

ensure_minio() {
  if helm status minio >/dev/null 2>&1; then
    echo "MinIO release already exists, restarting deployment to refresh configuration..."
    kubectl rollout restart deployment minio
    kubectl rollout status deployment minio --timeout=2m
  else
    echo "Installing MinIO via Helm chart..."
    helm install minio bitnami/minio \
      --version 17.0.21 \
      --set image.repository=bitnamilegacy/minio \
      --set console.image.repository=bitnamilegacy/minio-object-browser \
      --set console.service.type=NodePort \
      --set console.service.nodePorts.http=30090 \
      --set auth.rootUser=admin \
      --set auth.rootPassword=temporal \
      --set defaultBuckets='temporal-archival temporal-visibility' \
      --wait --timeout 5m
  fi
}

ensure_prometheus() {
  if helm status cluster-1-temporal-prometheus >/dev/null 2>&1; then
    # No --wait: verify_prometheus_targets owns confirming health (with its
    # own retry), same reasoning as ensure_postgres above.
    echo "Prometheus release already exists, upgrading..."
    helm upgrade cluster-1-temporal-prometheus prometheus-community/prometheus \
        -f prometheus/prometheus-values.yaml \
        --reuse-values
  else
    echo "Installing Prometheus via Helm chart..."
    helm install cluster-1-temporal-prometheus prometheus-community/prometheus \
        -f prometheus/prometheus-values.yaml \
        --wait --timeout 5m
  fi
}

ensure_grafana() {
  if helm status cluster-1-temporal-grafana >/dev/null 2>&1; then
    # No --wait: verify_grafana_dashboards owns confirming health (with its
    # own retry), same reasoning as ensure_postgres above.
    echo "Refreshing Grafana dashboards..."
    helm upgrade cluster-1-temporal-grafana grafana-community/grafana --version 12.7.3 \
        -f grafana/grafana-values.yaml \
        "${GRAFANA_DASHBOARD_FILES[@]}"
    echo "Dashboards refreshed successfully!"
  else
    echo "Installing Grafana via Helm chart..."
    helm install cluster-1-temporal-grafana grafana-community/grafana --version 12.7.3 \
        -f grafana/grafana-values.yaml \
        "${GRAFANA_DASHBOARD_FILES[@]}" \
        --wait --timeout 5m
  fi
}

# Grants the "temporal" app role full access to the given databases ($2+) for
# release $1. Split out from ensure_temporal_release so verify_temporal_health
# can reapply grants on their own -- e.g. if they get revoked on an
# already-installed release -- without that requiring a full reinstall.
ensure_temporal_db_grants() {
  local release="$1"
  shift
  local dbs=("$@")
  if ((${#dbs[@]} == 0)); then
    return
  fi

  echo "Ensuring the temporal role has full access to $release's databases..."
  for db in "${dbs[@]}"; do
    kubectl exec postgresql-0 -- env PGPASSWORD=temporal psql -U postgres -d "$db" -c "
      GRANT ALL PRIVILEGES ON DATABASE $db TO temporal;
      GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO temporal;
      GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO temporal;
      ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO temporal;
      ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO temporal;
    "
  done
}

# Ensures a Temporal Helm release ($1, e.g. "cluster-1") is installed or
# upgraded from its values file ($2). Any remaining args are database names
# that need the cluster-2-style Postgres-grants fix applied -- but only on a
# fresh install, since that's the path where the schema-setup hook has been
# observed (live, not assumed) to create tables under the Postgres superuser
# instead of the "temporal" app role, which denies the server pods
# permission to read schema_version and they'd never become ready.
ensure_temporal_release() {
  local release="$1" values_file="$2"
  shift 2
  local dbs=("$@")

  if helm status "$release" >/dev/null 2>&1; then
    # No --wait: verify_temporal_health owns confirming health (with its own
    # retry, including reapplying grants) so an unhealthy existing release
    # can't hang here and take the whole script down with it via `set -e`.
    echo "Reloading Temporal ($release)..."
    helm upgrade "$release" temporalio/temporal --version 1.6.0 \
        -f "$values_file" \
        --reuse-values
    echo "Temporal $release reloaded successfully!"
  else
    echo "Installing Temporal ($release) via Helm chart..."
    # No --wait here: the grants fix below (when applicable) must run
    # before the server pods can become ready.
    helm install "$release" temporalio/temporal --version 1.6.0 \
        -f "$values_file"

    # Guarded on length rather than expanding "${dbs[@]}" directly: on an
    # empty array, that expansion throws "unbound variable" under `set -u`
    # on bash 3.2 (macOS's default /bin/bash) even though bash 4.4+ treats
    # it as empty. Confirmed live running this script outside the
    # devcontainer, exactly the local-run case WORKSPACES_DIR exists for.
    if ((${#dbs[@]} > 0)); then
      ensure_temporal_db_grants "$release" "${dbs[@]}"
    fi

    kubectl rollout status "deployment/${release}-temporal-frontend" --timeout=3m
    kubectl rollout status "deployment/${release}-temporal-history" --timeout=3m
    kubectl rollout status "deployment/${release}-temporal-matching" --timeout=3m
    kubectl rollout status "deployment/${release}-temporal-worker" --timeout=3m
    echo "Temporal $release installed successfully!"
  fi
}

ensure_minio_bucket_policy() {
  echo "Configuring MinIO bucket policies for archival..."
  kubectl delete job minio-set-bucket-policy --ignore-not-found
  sleep 2
  kubectl apply -f minio/set-bucket-policies.yaml
}

# worker-control-ui (SYSTEMS-882) is this repo's own code, not a published
# image, so it's built and loaded into the k3d cluster directly rather than
# pulled from a registry -- same reasoning as the local Grafana dashboards
# being loaded via --set-file instead of a URL.
ensure_worker_control_ui() {
  echo "Building worker-control-ui images..."
  docker build -t worker-control-ui:local ./worker-control-ui
  # Derived from the upstream benchmark-workers image (see
  # worker-control-ui/runtime/Dockerfile) so the worker/runner Deployments'
  # containers have a shell -- lets the UI's "Kill" action exec `kill -9 1`
  # to crash the process in place instead of deleting the Pod.
  docker build -t worker-control-ui-runtime:local ./worker-control-ui/runtime
  echo "Importing worker-control-ui images into the k3d cluster..."
  k3d image import worker-control-ui:local worker-control-ui-runtime:local -c dev

  echo "Applying worker-control-ui manifests..."
  kubectl apply -f worker-control-ui/manifests.yaml

  # The image tag never changes across rebuilds, so `kubectl apply` alone
  # won't pick up new image content unless the pod spec itself changed --
  # force a fresh rollout so a rebuilt image is always picked up, same
  # reasoning as the forced restart in verify_temporal_health.
  kubectl rollout restart deployment/worker-control-ui
  kubectl rollout status deployment/worker-control-ui --timeout=2m
}

# Ensures Temporal namespace $2 is registered on release $1's cluster. The
# Helm chart doesn't auto-register any namespace (its `namespaceDefaults`
# key only configures settings *applied to* namespaces on creation, not
# create one) -- worker-control-ui and the CLI examples in README.md both
# assume a "default" namespace exists, so it must be registered explicitly.
ensure_temporal_namespace() {
  local release="$1" namespace="$2"
  if kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator namespace describe -n "$namespace" >/dev/null 2>&1; then
    return
  fi
  echo "Registering Temporal namespace '$namespace' on $release..."
  kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator namespace create -n "$namespace"
}

# omes' built-in kitchen-sink scenarios (the Runner UI's "Many Timers" /
# "Fan-Out" rate-mode options) require these search attributes on the target
# namespace before their very first workflow start. Confirmed live: without
# them pre-registered, the first several seconds of a fresh run fail with
# "no mapping defined for search attribute OmesExecutionID" while the
# mapping propagates -- it self-heals only because our Runner Deployment
# runs indefinitely, so pre-registering here avoids that error window.
ensure_omes_search_attributes() {
  local release="$1" namespace="$2"
  if kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator search-attribute list -n "$namespace" 2>/dev/null | grep -q OmesExecutionID; then
    return
  fi
  echo "Registering omes search attributes on $release/$namespace..."
  kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator search-attribute create -n "$namespace" --name OmesExecutionID --name KS_Keyword --type Keyword
  kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator search-attribute create -n "$namespace" --name KS_Int --type Int
}

#######################################
# verify_* functions: check real state (not just Deployment readiness),
# attempt one remediation on failure, then fail loudly if still broken.
#######################################

verify_postgres() {
  echo "Verifying PostgreSQL is ready..."
  if kubectl exec postgresql-0 -- pg_isready -U postgres >/dev/null 2>&1; then
    echo "PostgreSQL is ready!"
    return
  fi

  echo "PostgreSQL not ready -- retrying..."
  ensure_postgres
  if ! kubectl exec postgresql-0 -- pg_isready -U postgres >/dev/null 2>&1; then
    echo "ERROR: PostgreSQL is still not ready after retry." >&2
    exit 1
  fi
  echo "PostgreSQL ready on retry!"
}

verify_minio_bucket_policy() {
  echo "Verifying MinIO bucket policy job..."
  if kubectl wait --for=condition=complete job/minio-set-bucket-policy --timeout=60s >/dev/null 2>&1; then
    echo "MinIO bucket policies applied successfully!"
    return
  fi

  echo "MinIO bucket policy job did not complete cleanly -- logs:"
  kubectl logs job/minio-set-bucket-policy --all-containers 2>&1 || true

  echo "Retrying MinIO bucket policy job..."
  ensure_minio_bucket_policy
  if ! kubectl wait --for=condition=complete job/minio-set-bucket-policy --timeout=60s >/dev/null 2>&1; then
    echo "ERROR: MinIO bucket policy job failed after retry. Logs:" >&2
    kubectl logs job/minio-set-bucket-policy --all-containers 2>&1 || true
    exit 1
  fi
  echo "MinIO bucket policies applied successfully on retry!"
}

verify_grafana_dashboards() {
  echo "Verifying Grafana dashboards provisioned..."
  local found
  # grep -o | wc -l counts occurrences (grep -c would count matching LINES,
  # which is wrong here since the whole JSON response is one line). The
  # trailing `|| true` covers the zero-match case, where grep alone exits 1
  # and would trip `set -e` (via pipefail) before the count is even checked.
  found=$(curl -s -u admin:temporal 'http://localhost:3000/api/search?type=dash-db' | grep -o '"uid"' | wc -l | tr -d ' ' || true)

  if ((found >= EXPECTED_GRAFANA_DASHBOARD_COUNT)); then
    echo "Grafana dashboards provisioned successfully ($found found)!"
    return
  fi

  echo "Only $found/$EXPECTED_GRAFANA_DASHBOARD_COUNT Grafana dashboards provisioned -- retrying..."
  ensure_grafana
  found=$(curl -s -u admin:temporal 'http://localhost:3000/api/search?type=dash-db' | grep -o '"uid"' | wc -l | tr -d ' ' || true)

  if ((found < EXPECTED_GRAFANA_DASHBOARD_COUNT)); then
    echo "ERROR: Grafana only provisioned $found/$EXPECTED_GRAFANA_DASHBOARD_COUNT dashboards after retry." >&2
    exit 1
  fi
  echo "Grafana dashboards provisioned successfully on retry ($found found)!"
}

# Prometheus has no host port mapping (only Grafana/MinIO/Temporal do), so
# open a short-lived port-forward to query its API, then tear it down. Prints
# "unreachable" if the API itself couldn't be reached, otherwise the count of
# targets reporting down -- these must stay distinguishable, since 0 is also
# the healthy value and a totally unreachable Prometheus must not read as
# "0 targets down".
prometheus_target_status() {
  local pf_port=19090
  kubectl port-forward svc/cluster-1-temporal-prometheus-server "${pf_port}:80" >/dev/null 2>&1 &
  local pf_pid=$!
  sleep 3

  local response
  response=$(curl -s "http://localhost:${pf_port}/api/v1/targets" || true)

  kill "$pf_pid" 2>/dev/null || true
  wait "$pf_pid" 2>/dev/null || true

  if [[ "$response" != *'"status":"success"'* ]]; then
    echo "unreachable"
    return
  fi

  # grep -c exits 1 on a zero count -- `|| true` keeps that from tripping
  # `set -e` when there are (correctly) no down targets to report.
  grep -c '"health":"down"' <<<"$response" || true
}

verify_worker_control_ui() {
  echo "Verifying worker-control-ui is reachable..."
  if curl -sf http://localhost:8090/healthz >/dev/null 2>&1; then
    echo "worker-control-ui is healthy!"
    return
  fi

  echo "worker-control-ui not reachable -- retrying..."
  ensure_worker_control_ui
  if ! curl -sf http://localhost:8090/healthz >/dev/null 2>&1; then
    echo "ERROR: worker-control-ui is still not reachable after retry." >&2
    exit 1
  fi
  echo "worker-control-ui healthy on retry!"
}

verify_prometheus_targets() {
  echo "Verifying Prometheus scrape targets..."
  local status
  status=$(prometheus_target_status)

  if [[ "$status" == "0" ]]; then
    echo "Prometheus scrape targets are healthy!"
    return
  fi

  echo "Prometheus targets status: '$status' -- retrying..."
  ensure_prometheus
  status=$(prometheus_target_status)

  if [[ "$status" != "0" ]]; then
    echo "ERROR: Prometheus scrape targets still unhealthy ('$status') after retry." >&2
    exit 1
  fi
  echo "Prometheus scrape targets healthy on retry!"
}

# Runs rollout status for all four deployments of $1, returning non-zero if
# any of them fails/times out -- used with `if`/`&&` so a stuck rollout (e.g.
# CrashLoopBackOff) feeds into the self-heal path below instead of tripping
# `set -e` and killing the whole script before that path is ever reached.
temporal_deployments_ready() {
  local release="$1"
  kubectl rollout status "deployment/${release}-temporal-frontend" --timeout=3m &&
  kubectl rollout status "deployment/${release}-temporal-history" --timeout=3m &&
  kubectl rollout status "deployment/${release}-temporal-matching" --timeout=3m &&
  kubectl rollout status "deployment/${release}-temporal-worker" --timeout=3m
}

# Deployment readiness alone isn't enough -- a real bug hit live where
# cluster-2's Temporal pods came up Running-but-CrashLoopBackOff (a Postgres
# permissions issue) that rollout status didn't catch cleanly. Follow it up
# with an actual functional call against the cluster.
verify_temporal_health() {
  local release="$1"
  echo "Verifying Temporal ($release) health..."

  if temporal_deployments_ready "$release" &&
     kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator cluster health >/dev/null 2>&1; then
    echo "Temporal $release is healthy!"
    return
  fi

  echo "Temporal $release failed its cluster health check -- retrying..."
  case "$release" in
    cluster-1)
      ensure_temporal_release cluster-1 helm/cluster-1-temporal-values.yaml
      ;;
    cluster-2)
      # Reapply grants directly first: if cluster-2's release already exists,
      # ensure_temporal_release takes the upgrade path and won't touch grants
      # on its own, but a revoked grant is exactly the failure mode this
      # check exists to catch.
      ensure_temporal_db_grants cluster-2 temporal_persistence_2 temporal_visibility_2
      ensure_temporal_release cluster-2 helm/cluster-2-temporal-values.yaml temporal_persistence_2 temporal_visibility_2
      ;;
    *) echo "ERROR: unknown Temporal release '$release'." >&2; exit 1 ;;
  esac

  # Force a fresh rollout: pods that were already crash-looping can be deep
  # into Kubernetes' exponential CrashLoopBackOff delay (up to 5m between
  # attempts) and won't retry -- and so won't reflect the fix above -- for
  # minutes on their own. A fresh restart bypasses that backoff instead of
  # waiting on it. Confirmed live: reapplying grants alone left already-
  # crashing pods sitting in backoff long enough for the recheck below to
  # time out even though the underlying permissions were already fixed.
  kubectl rollout restart \
    "deployment/${release}-temporal-frontend" \
    "deployment/${release}-temporal-history" \
    "deployment/${release}-temporal-matching" \
    "deployment/${release}-temporal-worker"

  if ! temporal_deployments_ready "$release" ||
     ! kubectl exec "deploy/${release}-temporal-admintools" -- temporal operator cluster health >/dev/null 2>&1; then
    echo "ERROR: Temporal $release is still unhealthy after retry." >&2
    exit 1
  fi
  echo "Temporal $release healthy on retry!"
}

#######################################
# Main
#######################################

if [[ ! -d ./benchmark-workers ]]; then
  echo "Cloning benchmark-workers repository..."
  git clone https://github.com/temporalio/benchmark-workers.git
fi

helm repo add grafana-community https://grafana-community.github.io/helm-charts
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo add temporalio https://temporalio.github.io/helm-charts

ensure_k3d_cluster

if [[ ! -d "$WORKSPACES_DIR/.temporal" ]]; then
  mkdir -p "$WORKSPACES_DIR/.temporal"
  chmod 777 "$WORKSPACES_DIR/.temporal"
fi

ensure_postgres
ensure_minio
ensure_prometheus
ensure_grafana
ensure_temporal_release cluster-1 helm/cluster-1-temporal-values.yaml
ensure_temporal_release cluster-2 helm/cluster-2-temporal-values.yaml temporal_persistence_2 temporal_visibility_2
ensure_minio_bucket_policy
ensure_worker_control_ui

echo ""
echo "Running post-start validation..."
# Order matters: verify_temporal_health runs before verify_prometheus_targets
# because an unhealthy Temporal pod shows up as a down scrape target too --
# fixing the root cause first means that symptom clears on its own, instead
# of verify_prometheus_targets futilely retrying an upgrade of Prometheus
# itself (which was never the actual problem).
verify_postgres
verify_temporal_health cluster-1
ensure_temporal_namespace cluster-1 default
ensure_temporal_namespace cluster-1 workshop
ensure_omes_search_attributes cluster-1 workshop
verify_temporal_health cluster-2
ensure_temporal_namespace cluster-2 default
ensure_temporal_namespace cluster-2 workshop
ensure_omes_search_attributes cluster-2 workshop
verify_minio_bucket_policy
verify_grafana_dashboards
verify_prometheus_targets
verify_worker_control_ui

echo ""
echo "All services up and validated successfully!"
