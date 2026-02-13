# Temporal Self-Hosting Workshop (Podman Edition)

This workshop demonstrates running Temporal with multi-cluster replication using **Podman** (or Docker) with Docker Compose - no Kubernetes required!

## Architecture

The workshop sets up:
- **Two Temporal clusters** (cluster-1 and cluster-2) with cross-cluster replication
- **PostgreSQL** with separate databases for each cluster
- **Grafana** for observability and metrics
- **Prometheus** for metrics collection
- **Archival** using shared file storage
