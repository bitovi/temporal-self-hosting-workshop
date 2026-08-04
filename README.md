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
| Codec Server | `8091` | Codec Server *(not deployed until `_scripts/start-codec-demo.sh` runs — see below)* |
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

This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. A worker polls the `codec-demo` task queue in the `default` namespace.

Port `8091` is forwarded as a **public** Codespaces port (see `.devcontainer/devcontainer.json`) so the Web UI's browser-side codec calls can reach it without a GitHub-auth redirect breaking the request — the codec server itself has no authentication (see `codec-demo/cmd/codec-server/main.go`'s CORS comment), so treat it as unauthenticated: don't route real sensitive data through this demo.

Codec-server/codec-worker aren't deployed automatically — when you're ready to explore this, run:

```bash
_scripts/start-codec-demo.sh
```

Once that completes, start either workflow below to generate encrypted history.

**Start the onboarding demo (encrypted workflow input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type CustomerOnboardingWorkflow \
  --input '{"name":"Jane Doe","ssn":"123-45-6789","email":"jane@example.com"}' \
  --codec-endpoint http://localhost:8091
```

**Start the payment demo (encrypted activity input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type ProcessPaymentWorkflow \
  --input '{"customerId":"cust-6789","cardDetails":{"cardNumber":"4111111111111111","cvv":"123","expMonth":12,"expYear":2030},"amount":49.99}' \
  --codec-endpoint http://localhost:8091
```

`--codec-endpoint` here makes the CLI encrypt the *input* through the codec server before submitting it, so the workflow's input payload is stored as ciphertext too (not just its result). If you omit it, the input is sent through the plain JSON converter and stored as readable JSON -- the codec's passthrough logic on the read side accepts either, decrypting payloads it recognizes and passing through ones it doesn't, which is what makes that omission silent instead of an error.

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
3. Set the endpoint: `http://localhost:8091` if you're using desktop VS Code; if you're in a browser-based Codespace, use the forwarded URL for port 8091 instead (Ports tab → right-click port `8091` → **Copy Local Address**). Save.
4. Reload the workflow's history — the payloads now decode live to readable JSON.
