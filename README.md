# Temporal Self-Hosting Workshop

## Prerequisites

- GitHub account

## Starting a Codespace

1. Navigate to this repository on GitHub
2. Click the **Code** button → select the **Codespaces** tab
3. Click **Create codespace on main**
4. Wait for the environment to finish building — this takes a few minutes on first launch as it provisions a k3d cluster, PostgreSQL, MinIO, and Temporal

> The setup runs automatically via `_scripts/start.sh`. Once complete, the services listed below are available.

## Service Addresses

Services are accessible via the **Ports** tab in the Codespace editor (or the forwarded URLs shown there).

| Service | Local Port | Ports Tab Label |
|---|---|---|
| Temporal WebUI (cluster-1) | `8080` | WebUI |
| Temporal WebUI (standby cluster) | `8181` | WebUI for Standby |
| Grafana | `3000` | Grafana |
| MinIO S3 WebUI | `9090` | S3 WebUI for MinIO |
| Temporal gRPC frontend | `7233` | *(used by CLI)* |

> In the Ports tab, right-click a port and select **Open in Browser** to access the UI.

## Temporal CLI

The `temporal` CLI is pre-installed in the Codespace. Open a terminal and use it directly — it defaults to `localhost:7233`.

**List namespaces:**
```bash
temporal operator namespace list
```

**List workflows in a namespace:**
```bash
temporal workflow list --namespace default
```

**Start a workflow:**
```bash
temporal workflow start \
  --namespace default \
  --task-queue my-task-queue \
  --type MyWorkflow \
  --input '"hello"'
```

**Describe a workflow execution:**
```bash
temporal workflow describe --workflow-id <workflow-id> --namespace default
```

**Show workflow history:**
```bash
temporal workflow show --workflow-id <workflow-id> --namespace default
```

**Connect to a specific address (e.g., standby cluster on port 8233):**
```bash
temporal --address localhost:8233 operator namespace list
```
