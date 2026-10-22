# Temporal Self-Hosting Workshop

## Prerequisites

- GitHub account

## Starting a Codespace

1. Navigate to this repository on GitHub
2. Click the **Code** button → select the **Codespaces** tab
3. Click **Create codespace on main**
4. Wait for the environment to finish building — this takes a few minutes on first launch as it provisions a k3d cluster, PostgreSQL, MinIO, and Temporal

> The setup runs automatically via `_scripts/start.sh`. Once complete, the services listed below are available.

If the environment ever comes back from idle looking unhealthy, run `_scripts/restart.sh` to cycle every pod and re-validate the whole stack — Temporal's workflow history and archived data both survive it (see the script's own comments for why).

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

This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. A worker polls the `codec-demo` task queue in the `default` namespace.

Port `8091` is forwarded as a **public** Codespaces port (see `.devcontainer/devcontainer.json`) so the Web UI's browser-side codec calls can reach it without a GitHub-auth redirect breaking the request — the codec server itself has no authentication (see `codec-demo/cmd/codec-server/main.go`'s CORS comment), so treat it as unauthenticated: don't route real sensitive data through this demo.

Codec-server is deployed automatically by `_scripts/start.sh`, same as the rest of the stack. codec-worker is deployed too, but starts at 0 replicas -- turn it on in the Worker Control UI (port `8090`, **Codec Demo Worker** toggle) before starting either workflow below.

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

## Workshop Visuals

Interactive diagrams used during the workshop live in [`docs/visuals/`](docs/visuals/) and are published with GitHub Pages at **https://bitovi.github.io/temporal-self-hosting-workshop/**, so they open in any browser with no Codespace needed. Every page has an **All visuals** menu (`M`) for moving between them:

- [Task queue separation](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/task-queue-separation.html): how long-running activities hold worker slots and stall fast work, and why separate task queues fix it. Includes a sandbox for "what if" questions.
- [Request flow](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/request-flow.html): one workflow and one activity, step by step through Frontend, History, Matching and the database, including replay after a worker crash.
- [Codec server](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/codec-server.html): why mTLS isn't enough for data residency, where the payload codec runs, and what the codec server is for. Companion to the Codec Server exercise above.
- [History shards](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/history-shards.html): why shard count and database latency set a hard throughput ceiling, why adding History hosts doesn't raise it, and how one busy workflow creates a hot shard. Includes a sandbox.
- [Workflows vs activities](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/workflows-vs-activities.html): what happens when things fail before, during and after an activity, why activities need idempotency keys, and why workflow code must be deterministic.
- [Zero-downtime upgrade](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/zero-downtime-upgrade.html): upgrade the standby cluster's schema and server, hand the namespace over, then upgrade the other side, with workers running the whole time.
- [Failover versions](https://bitovi.github.io/temporal-self-hosting-workshop/visuals/failover-versioning.html): how multi-cluster replication stamps every event with a failover version, what failover does to it, and how conflicts after an async-replication gap are resolved.
