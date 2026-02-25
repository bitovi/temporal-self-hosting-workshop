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
  
  echo "Reloading Temporal"
  helm upgrade cluster-1 temporalio/temporal --version 0.73.1 \
      -f helm/cluster-1-temporal-values.yaml \
      --reuse-values \
      --wait --timeout 2m
  echo "Temporal reloaded successfully!"
  
  # Restart Grafana pod to pick up dashboard changes
  echo "Restarting Grafana pod to refresh dashboards..."
  kubectl rollout restart deployment cluster-1-temporal-grafana
  kubectl rollout status deployment cluster-1-temporal-grafana --timeout=2m
  echo "Dashboards restarted successfully!"

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
  k3d kubeconfig get dev > /workspaces/.kube/dev.yaml

  chmod 666 /workspaces/.kube/dev.yaml

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

  if [[ ! -d /workspaces/.temporal ]]; then
    mkdir -p /workspaces/.temporal
    chmod 777 /workspaces/.temporal
  fi

  helm template temporal temporalio/temporal --version 0.73.1 \
      -f helm/cluster-1-temporal-values.yaml > ./manifest.yaml

  # Install Temporal using Helm
  echo "Installing Temporal via Helm chart..."
  helm install cluster-1 temporalio/temporal --version 0.73.1 \
      -f helm/cluster-1-temporal-values.yaml
  
  # Wait for Temporal to be ready
  echo "Waiting for Temporal services to be ready..."
  kubectl rollout status deployment/cluster-1-temporal-frontend --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-history --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-matching --timeout=3m
  kubectl rollout status deployment/cluster-1-temporal-worker --timeout=3m
  
  # Configure MinIO bucket policies
  echo "Configuring MinIO bucket policies for archival..."
  kubectl apply -f minio/set-bucket-policies.yaml
  kubectl wait --for=condition=complete job/minio-set-bucket-policy --timeout=60s || true
fi
