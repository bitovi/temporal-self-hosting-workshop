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
| Worker Control UI | `8090` | Worker Control UI |
| Codec Server | `8091` | Codec Server |
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

## Codec Server

This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. Two demo workflows run continuously on the `codec-demo` task queue in the `default` namespace, so their history is genuinely unreadable in the raw Web UI/CLI output until you point a client at the codec server.

**Start the onboarding demo (encrypted workflow input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type CustomerOnboardingWorkflow \
  --input '{"name":"Jane Doe","ssn":"123-45-6789","email":"jane@example.com"}'
```

**Start the payment demo (encrypted activity input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type ProcessPaymentWorkflow \
  --input '{"customerId":"cust-6789","cardDetails":{"cardNumber":"4111111111111111","cvv":"123","expMonth":12,"expYear":2030},"amount":49.99}'
```

**See the raw ciphertext (no codec configured):**

```bash
temporal workflow show --workflow-id <workflow-id> --namespace default
```

The `Input`/`Result` fields will show base64 ciphertext, not readable JSON.

**Decode it via the CLI:**

```bash
temporal workflow show --workflow-id <workflow-id> --namespace default \
  --codec-endpoint http://localhost:8091
```

**Decode it in the Web UI:**

1. Open the Web UI (port `8080`) and navigate to the workflow's history — the input/result will show as ciphertext.
2. Open the namespace's **Codec Server** setting (in the Web UI's settings for the `default` namespace).
3. Set the endpoint to `http://localhost:8091` and save.
4. Reload the workflow's history — the payloads now decode live to readable JSON.
