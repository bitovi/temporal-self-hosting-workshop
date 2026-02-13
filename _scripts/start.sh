#! /bin/bash
set -euo pipefail

if [[ ! -d ./benchmark-workers ]]; then
  echo "Cloning benchmark-workers repository..."
  git clone https://github.com/temporalio/benchmark-workers.git
fi

# Start the k3d cluster
if k3d cluster list | grep -q '^dev\s'; then
  echo "k3d cluster 'dev' already exists"
  k3d cluster start dev
  
  # Ensure dashboards are reloaded after restart
  echo "Reloading Grafana dashboards..."
  helm repo add temporalio https://temporalio.github.io/helm-charts 2>/dev/null || true
  helm upgrade cluster-1 temporalio/temporal --version 0.68.1 \
      -f helm/cluster-1-temporal-values.yaml \
      --reuse-values \
      --wait --timeout 2m
  
  # Restart Grafana pod to pick up dashboard changes
  echo "Restarting Grafana pod to refresh dashboards..."
  kubectl rollout restart deployment cluster-1-temporal-grafana
  kubectl rollout status deployment cluster-1-temporal-grafana --timeout=2m
  
  echo "Dashboards reloaded successfully!"
else
  mkdir -p /tmp/k3d-storage

  # Map host ports -> cluster nodeports
  k3d cluster create dev \
    -p "8080:30080@server:0" \
    -p "7233:30233@server:0" \
    -p "3000:30000@server:0" \
    -p "8233:31233@server:0" \
    -p "8181:31080@server:0" \
    --volume /tmp/k3d-storage:/mnt/storage@all \
    --wait --timeout 120s

  # Generate the kubeconfig for the cluster
  k3d kubeconfig get dev > /workspaces/.kube/dev.yaml

  chmod 666 /workspaces/.kube/dev.yaml

  docker exec k3d-dev-server-0 mkdir -p /mnt/storage/temporal_archival
  docker exec k3d-dev-server-0 chmod 777 /mnt/storage/temporal_archival
  kubectl apply -f infra/shared-volume.yml

  helm repo add bitnami https://charts.bitnami.com/bitnami
  echo "Installing PostgreSQL via Helm chart..."
  helm install postgresql bitnami/postgresql --version 18.1.2 \
      -f helm/postgres-values.yaml \
      --wait --timeout 5m

  # Add the Temporal Helm repository
  helm repo add temporalio https://temporalio.github.io/helm-charts

  if [[ ! -d /workspaces/.temporal ]]; then
    mkdir -p /workspaces/.temporal
    chmod 777 /workspaces/.temporal
  fi

  helm template temporal temporalio/temporal --version 0.68.1 \
      -f helm/cluster-1-temporal-values.yaml > ./manifest.yaml

  # Install Temporal using Helm
  echo "Installing Temporal via Helm chart..."
  helm install cluster-1 temporalio/temporal --version 0.68.1 \
      -f helm/cluster-1-temporal-values.yaml
fi
