# Codec Server Demo — Design

## Goal

Add a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) to the workshop, plus two demo workflows whose payloads are genuinely encrypted, so learners see:

1. Ciphertext by default in the Web UI / CLI history (raw Payloads are AES-256-GCM encrypted).
2. The same history decoded live once they point the Web UI's per-namespace "Codec Server" setting (or the CLI's `--codec-endpoint` flag) at the codec server.

This demonstrates why self-hosted Temporal deployments use a codec server: sensitive workflow/activity data can be encrypted at rest and in transit through the Temporal cluster itself, decodable only by clients that go through the codec server.

## Repo layout

New top-level directory, one Go module (mirrors `worker-control-ui`'s self-contained-module pattern):

```
codec-demo/
  go.mod
  codec/
    aesgcm.go          # shared PayloadCodec: Encode/Decode (AES-256-GCM)
    aesgcm_test.go     # roundtrip unit test
  cmd/
    codec-server/main.go   # HTTP codec server (Temporal's codec-server contract)
    worker/main.go         # Temporal worker: registers both workflows + the activity
  workflows/
    onboarding.go      # CustomerOnboardingWorkflow
    payment.go         # ProcessPaymentWorkflow + ChargeCardActivity
  Dockerfile           # multi-stage build, produces both binaries
  manifests.yaml       # Secret (AES key) + Deployment x2 + Service (codec-server only)
```

The `codec/` package is shared by both binaries so the worker (which encrypts/decrypts for real execution) and the codec server (which only decrypts for human viewing) can never drift out of wire-compatibility — a real risk if the AES-GCM nonce/tag handling were implemented twice.

## Codec design (encryption)

- **Algorithm:** AES-256-GCM, Temporal's own documented reference pattern for `go.temporal.io/sdk/converter.PayloadCodec`. `Encode` generates a random 12-byte nonce per call, encrypts the payload's serialized bytes, and prepends the nonce to the ciphertext. `Decode` splits the nonce back off before decrypting.
- **Key management:** a single static 32-byte AES key, base64-encoded, committed directly in `codec-demo/manifests.yaml` as a Kubernetes `Secret` — mounted as env var `CODEC_AES_KEY` into **both** the `codec-server` and `worker` Deployments. This follows the repo's existing convention of hardcoded demo credentials (e.g. MinIO's `admin`/`temporal`). The Secret manifest will comment clearly that this is a static demo-only value, not for production use.
- **Scope:** the codec wraps every `Payload` that crosses the wire — workflow input, workflow result, activity input, activity result — via the SDK's uniform `DataConverter` hook, so no per-workflow special-casing is needed.

## Codec HTTP server

Deployed as `codec-server` (Deployment + NodePort Service, same pattern as `worker-control-ui`):

- `POST /decode` and `POST /encode` — Temporal's standard codec-server contract: body `{"payloads":[...]}`, response is the transformed list in the same shape. The Web UI and CLI both call `/decode` once a codec endpoint is configured.
- CORS: permissive (`Access-Control-Allow-Origin: *`), allowing the `Content-Type` and `X-Namespace` headers the Web UI sends — consistent with running with no auth, so the browser-based Web UI can call it directly from `localhost:8080`.
- `GET /healthz` — same readiness/liveness + `verify_*` pattern as `worker-control-ui`.
- No authentication (explicit choice for this workshop — simplicity over the realistic-but-more-complex shared-secret-header model).

## Demo workflows & worker

Deployed as `worker` (Deployment only, no Service — a long-running task-queue poller), registered on task queue `codec-demo`, namespace `default`, using a `DataConverter` wrapping the shared `codec/` package. Not auto-started by `start.sh` — learners trigger them via `temporal workflow start` CLI commands documented in the README.

1. **`CustomerOnboardingWorkflow`**
   - Input: `CustomerInfo{Name, SSN, Email}`
   - Output: `OnboardingResult{CustomerID, Status}`
   - No activities. Demonstrates encryption of top-level **workflow** input/result.

2. **`ProcessPaymentWorkflow`**
   - Input: `PaymentRequest{CustomerID string, CardDetails CardDetails}` where `CardDetails{CardNumber, CVV, ExpMonth, ExpYear}`
   - Calls activity **`ChargeCardActivity(CardDetails) (ChargeReceipt, error)`** where `ChargeReceipt{ReceiptID, Last4, Amount}`
   - Output: `PaymentResult{ReceiptID, Status}`
   - Demonstrates encryption of **activity** input/result as a distinct payload surface from the workflow's own input/result.

## Deployment wiring

- **`_scripts/start.sh`:**
  - New `ensure_codec_demo()`: build `codec-demo:local` image, `k3d image import`, `kubectl apply -f codec-demo/manifests.yaml`, forced rollout restart of both Deployments (mirrors `ensure_worker_control_ui`).
  - New `verify_codec_demo()`: curl the codec server's `/healthz` through its host port, plus `kubectl rollout status` on both Deployments; one retry via `ensure_codec_demo`, then hard-fail (mirrors `verify_worker_control_ui`).
  - Both called from `main` alongside the existing `ensure_worker_control_ui` / `verify_worker_control_ui` calls.
- **`ensure_k3d_cluster`:** add a new host port mapping for the codec server's NodePort: `8091:30091@server:0` (next free port after 8080/7233/3000/8233/8181/9090/8090).
- **`.devcontainer/devcontainer.json`:** add `8091` to `forwardPorts` and a `portsAttributes` entry labeled `"Codec Server"`.
- **`README.md`:**
  - New row in the Service Addresses table for the Codec Server port (`8091` / `Codec Server`).
  - New "Codec Server" section: what it is and why, the two workflow names + task queue, example `temporal workflow start` commands with sample JSON input for each, then a step-by-step: view the workflow in the Web UI (raw ciphertext) → open the namespace's Codec Server setting → paste `http://localhost:8091` → watch it decode live; plus the CLI equivalent (`temporal workflow show ...` vs. `... --codec-endpoint http://localhost:8091`).

Note: an already-running `dev` k3d cluster won't pick up the new port mapping without being recreated — a pre-existing limitation of this script for any port-list change, not something new introduced here.

## Testing / verification

The repo has no existing Go test suite anywhere; correctness today is entirely via `start.sh`'s live `verify_*` checks. This design keeps that convention, with one exception:

- **`codec/aesgcm_test.go`** — roundtrip unit test (`Decode(Encode(x)) == x`, plus a check that ciphertext differs from plaintext). Crypto code is worth a real test even though nothing else in the repo has one, since a subtle bug (nonce reuse, truncation) won't necessarily surface via a manual smoke test.
- Everything else (HTTP handlers, workflow/activity logic, deployment) is verified live via `verify_codec_demo()`.
- Manual acceptance check before calling this done: run `start.sh`, start both demo workflows via the README's CLI commands, confirm ciphertext appears in the Web UI / `temporal workflow show` by default, and confirm pasting the codec endpoint (Web UI) / `--codec-endpoint` flag (CLI) decodes it correctly for both the workflow-level and activity-level payloads.
