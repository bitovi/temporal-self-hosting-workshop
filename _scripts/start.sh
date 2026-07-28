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

if [[ ! -d ./benchmark-workers ]]; then
  echo "Cloning benchmark-workers repository..."
  git clone https://github.com/temporalio/benchmark-workers.git
fi

# Start the k3d cluster
if k3d cluster list | grep -q '^dev\s'; then
  echo "k3d cluster 'dev' already exists"
  k3d cluster start dev
  
  echo "Reloading Temporal"
  helm upgrade cluster-1 temporalio/temporal --version 1.6.0 \
      -f helm/cluster-1-temporal-values.yaml \
      --reuse-values \
      --wait --timeout 2m
  echo "Temporal cluster-1 reloaded successfully!"

  # Older k3d clusters (from before cluster-2 was started by default) won't
  # have a cluster-2 release to reuse values from -- fall back to a fresh
  # install in that case instead of upgrading.
  if helm status cluster-2 >/dev/null 2>&1; then
    helm upgrade cluster-2 temporalio/temporal --version 1.6.0 \
        -f helm/cluster-2-temporal-values.yaml \
        --reuse-values \
        --wait --timeout 2m
  else
    # No --wait here: cluster-2's schema-setup hook sometimes creates its
    # databases/tables under the Postgres superuser rather than the
    # "temporal" app role (root cause not fully pinned down -- observed live
    # rather than assumed), which denies the server pods permission to read
    # schema_version and they'd never become ready. Grant explicitly before
    # waiting on rollout instead of trusting the chart got ownership right.
    helm install cluster-2 temporalio/temporal --version 1.6.0 \
        -f helm/cluster-2-temporal-values.yaml
    echo "Ensuring the temporal role has full access to cluster-2's databases..."
    for db in temporal_persistence_2 temporal_visibility_2; do
      kubectl exec postgresql-0 -- env PGPASSWORD=temporal psql -U postgres -d "$db" -c "
        GRANT ALL PRIVILEGES ON DATABASE $db TO temporal;
        GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO temporal;
        GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO temporal;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO temporal;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO temporal;
      "
    done
    kubectl rollout status deployment/cluster-2-temporal-frontend --timeout=2m
    kubectl rollout status deployment/cluster-2-temporal-history --timeout=2m
    kubectl rollout status deployment/cluster-2-temporal-matching --timeout=2m
    kubectl rollout status deployment/cluster-2-temporal-worker --timeout=2m
  fi
  echo "Temporal cluster-2 reloaded successfully!"

  # Re-apply Grafana so locally-edited dashboards (bitovi-temporal-dashboard,
  # benchmark-workshop-dashboard) are picked up -- a plain pod restart isn't
  # enough since their content is injected via --set-file, not fetched fresh
  # from a URL at pod startup.
  echo "Refreshing Grafana dashboards..."
  helm upgrade cluster-1-temporal-grafana grafana-community/grafana --version 12.7.3 \
      -f grafana/grafana-values.yaml \
      "${GRAFANA_DASHBOARD_FILES[@]}" \
      --wait --timeout 2m
  echo "Dashboards refreshed successfully!"

  # Restart MinIO pod to ensure it picks up any configuration changes
  echo "Restarting MinIO pod to refresh configuration..."
  kubectl rollout restart deployment minio
  kubectl rollout status deployment minio --timeout=2m
  echo "MinIO restarted successfully!"
  
  # Re-apply bucket policies
  echo "Configuring MinIO bucket policies for archival..."
  kubectl delete job minio-set-bucket-policy --ignore-not-found
  sleep 2
  kubectl apply -f minio/set-bucket-policies.yaml
  kubectl wait --for=condition=complete job/minio-set-bucket-policy --timeout=60s || true
  
  echo "Reload complete!"

else
  # Map host ports -> cluster nodeports
  k3d cluster create dev \
    -p "8080:30080@server:0" \
    -p "7233:30233@server:0" \
    -p "3000:30000@server:0" \
    -p "8233:31233@server:0" \
    -p "8181:31080@server:0" \
    -p "9090:30090@server:0" \
    --wait --timeout 120s

  # Generate the kubeconfig for the cluster
  mkdir -p "$WORKSPACES_DIR/.kube"
  k3d kubeconfig get dev > "$WORKSPACES_DIR/.kube/dev.yaml"

  chmod 666 "$WORKSPACES_DIR/.kube/dev.yaml"
  
  echo "Installing Grafana via Helm chart"
  helm repo add grafana-community https://grafana-community.github.io/helm-charts
  helm install cluster-1-temporal-grafana grafana-community/grafana --version 12.7.3 \
      -f grafana/grafana-values.yaml \
      "${GRAFANA_DASHBOARD_FILES[@]}" \
      --wait --timeout 5m

  echo "Installing Prometheus via Helm chart"
  helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
  helm install cluster-1-temporal-prometheus prometheus-community/prometheus \
      -f prometheus/prometheus-values.yaml \
      --wait --timeout 5m


  helm repo add bitnami https://charts.bitnami.com/bitnami
  echo "Installing PostgreSQL via Helm chart..."
  helm install postgresql bitnami/postgresql --version 18.1.2 \
      -f helm/postgres-values.yaml \
      --wait --timeout 5m
  
  echo "Installing MinIO via Helm chart..."
  helm install minio bitnami/minio \
    --version 17.0.21 \
    --set image.repository=bitnamilegacy/minio \
    --set console.image.repository=bitnamilegacy/minio-object-browser \
    --set console.service.type=NodePort \
    --set console.service.nodePorts.http=30090 \
    --set auth.rootUser=admin \
    --set auth.rootPassword=temporal \
    --set defaultBuckets='temporal-archival temporal-visibility'

  # Add the Temporal Helm repository
  helm repo add temporalio https://temporalio.github.io/helm-charts

  if [[ ! -d "$WORKSPACES_DIR/.temporal" ]]; then
    mkdir -p "$WORKSPACES_DIR/.temporal"
    chmod 777 "$WORKSPACES_DIR/.temporal"
  fi

  helm template temporal temporalio/temporal --version 1.6.0 \
      -f helm/cluster-1-temporal-values.yaml > ./manifest.yaml

  # Install Temporal using Helm
  echo "Installing Temporal via Helm chart..."
  helm install cluster-1 temporalio/temporal --version 1.6.0 \
      -f helm/cluster-1-temporal-values.yaml

  echo "Installing standby Temporal cluster (cluster-2) via Helm chart..."
  helm install cluster-2 temporalio/temporal --version 1.6.0 \
      -f helm/cluster-2-temporal-values.yaml

  # cluster-2's schema-setup hook sometimes creates its databases/tables
  # under the Postgres superuser rather than the "temporal" app role (root
  # cause not fully pinned down -- observed live rather than assumed), which
  # denies the server pods permission to read schema_version and they'd
  # never become ready. Grant explicitly before waiting on rollout instead
  # of trusting the chart got ownership right.
  echo "Ensuring the temporal role has full access to cluster-2's databases..."
  for db in temporal_persistence_2 temporal_visibility_2; do
    kubectl exec postgresql-0 -- env PGPASSWORD=temporal psql -U postgres -d "$db" -c "
      GRANT ALL PRIVILEGES ON DATABASE $db TO temporal;
      GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO temporal;
      GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO temporal;
      ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO temporal;
      ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO temporal;
    "
  done

  # Wait for Temporal to be ready
  echo "Waiting for Temporal services to be ready..."
  kubectl rollout status deployment/cluster-1-temporal-frontend --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-history --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-matching --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-worker --timeout=3m
  kubectl rollout status deployment/cluster-2-temporal-frontend --timeout=3m
  kubectl rollout status deployment/cluster-2-temporal-history --timeout=3m
  kubectl rollout status deployment/cluster-2-temporal-matching --timeout=3m
  kubectl rollout status deployment/cluster-2-temporal-worker --timeout=3m

  # Configure MinIO bucket policies
  echo "Configuring MinIO bucket policies for archival..."
  kubectl apply -f minio/set-bucket-policies.yaml
  kubectl wait --for=condition=complete job/minio-set-bucket-policy --timeout=60s || true
fi

