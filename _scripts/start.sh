#! /bin/bash
set -euo pipefail

if [[ ! -d ./benchmark-workers ]]; then
  echo "Cloning benchmark-workers repository..."
  git clone https://github.com/temporalio/benchmark-workers.git
fi

# Create necessary directories
if [[ ! -d /workspaces/.temporal ]]; then
  mkdir -p /workspaces/.temporal
  chmod 777 /workspaces/.temporal
fi

echo "Starting Temporal clusters with Docker Compose (Podman compatible)..."

# Check if containers are already running
if docker-compose ps | grep -q 'Up'; then
  echo "Temporal services are already running"
  docker-compose ps
else
  # Start all services with docker-compose
  echo "Starting PostgreSQL, Temporal clusters (1 & 2), Grafana, and Prometheus..."
  docker-compose up -d

  echo "Waiting for services to be healthy..."
  sleep 20

  # Wait for PostgreSQL
  echo "Waiting for PostgreSQL to be ready..."
  until docker exec temporal-postgresql pg_isready -U temporal > /dev/null 2>&1; do
    echo "Waiting for PostgreSQL..."
    sleep 2
  done

  echo "Waiting for Temporal cluster 1 frontend..."
  until docker exec cluster-1-frontend temporal operator cluster health 2>/dev/null | grep -q "SERVING"; do
    echo "Waiting for cluster-1..."
    sleep 3
  done

  echo "Waiting for Temporal cluster 2 frontend..."
  until docker exec cluster-2-frontend temporal operator cluster health 2>/dev/null | grep -q "SERVING"; do
    echo "Waiting for cluster-2..."
    sleep 3
  done

  echo ""
  echo "✓ All services started successfully!"
  echo ""
  echo "Services available at:"
  echo "  - Cluster 1 Frontend: localhost:7233"
  echo "  - Cluster 1 Web UI:   http://localhost:8080"
  echo "  - Cluster 2 Frontend: localhost:8233"
  echo "  - Cluster 2 Web UI:   http://localhost:8181"
  echo "  - Grafana:            http://localhost:3000 (admin/temporal)"
  echo "  - Prometheus:         http://localhost:9090"
  echo ""
fi
