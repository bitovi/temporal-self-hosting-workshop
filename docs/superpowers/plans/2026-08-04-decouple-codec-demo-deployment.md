# Decouple codec-demo Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move codec-server/codec-worker deployment out of `_scripts/start.sh`'s automatic pass into a new, manually-run `_scripts/start-codec-demo.sh`, so codec-demo no longer builds/deploys on every codespace creation.

**Architecture:** `start.sh` loses its `ensure_codec_demo`/`verify_codec_demo` functions and their two call sites — a clean cut, since codec-demo shares no state (env vars, namespaces, DB grants) with the rest of the script. A new, fully self-contained script carries its own copy of the same build → import → apply → rollout → health-check-with-one-retry logic. README gets a pointer to the new script.

**Tech Stack:** Bash (`set -euo pipefail`), `docker`, `k3d`, `kubectl`, `curl` — no new dependencies.

## Global Constraints

- No changes to `codec-demo/manifests.yaml`, the Dockerfile, or any Go source (per spec's "Out of scope").
- No changes to `.devcontainer/devcontainer.json` (per spec's "Out of scope").
- No teardown/undeploy script (per spec's "Out of scope").
- README note must not reference a specific day/schedule — phrase as "when you're ready" (per spec).
- No cluster access available to the plan's implementer in this environment — validate scripts with `bash -n` (syntax check) only; do not attempt to run them against a live cluster. Functional verification against a real Codespace is the user's responsibility.

---

### Task 1: Create `_scripts/start-codec-demo.sh`

**Files:**
- Create: `_scripts/start-codec-demo.sh`

**Interfaces:**
- Consumes: nothing from other tasks — fully self-contained. Assumes the `dev` k3d cluster already exists (created by `_scripts/start.sh`) and that `codec-demo/manifests.yaml` / `codec-demo/Dockerfile` are unchanged from their current form (Deployment names `codec-server` and `codec-worker`, Service exposing `8091`, health path `/health`).
- Produces: an executable script other humans/instructions (README, Task 3) reference by path `_scripts/start-codec-demo.sh`.

- [ ] **Step 1: Write the script**

Create `_scripts/start-codec-demo.sh` with this exact content (the `ensure`/`verify` bodies are moved verbatim from `_scripts/start.sh`'s current `ensure_codec_demo`/`verify_codec_demo`, just merged into one standalone file):

```bash
#! /bin/bash
set -euo pipefail

# Deploys the codec-demo codec server + worker into the already-running
# `dev` k3d cluster. Split out from start.sh because codec-demo is used
# later in the workshop than the rest of the stack, so it shouldn't have
# to build/deploy on every codespace creation -- run this manually
# whenever you're ready to explore it.

# codec-demo is this repo's own code, not a published image, so it's built
# and loaded into the k3d cluster directly rather than pulled from a
# registry -- same reasoning as worker-control-ui's build step in start.sh.
ensure_codec_demo() {
  echo "Building codec-demo image..."
  docker build -t codec-demo:local ./codec-demo
  echo "Importing codec-demo image into the k3d cluster..."
  k3d image import codec-demo:local -c dev

  echo "Applying codec-demo manifests..."
  kubectl apply -f codec-demo/manifests.yaml

  # The image tag never changes across rebuilds, so `kubectl apply` alone
  # won't pick up new image content unless the pod spec itself changed --
  # force a fresh rollout so a rebuilt image is always picked up.
  kubectl rollout restart deployment/codec-server deployment/codec-worker
  kubectl rollout status deployment/codec-server --timeout=2m
  kubectl rollout status deployment/codec-worker --timeout=2m
}

verify_codec_demo() {
  echo "Verifying codec-demo is reachable..."

  # /health alone only proves codec-server is up -- also check codec-worker's
  # rollout, since it's a separate Deployment that /health can't see and the
  # demo workflows silently never make progress if it's down.
  if curl -sf http://localhost:8091/health >/dev/null 2>&1 &&
     kubectl rollout status deployment/codec-worker --timeout=2m; then
    echo "codec-demo is healthy!"
    return
  fi

  echo "codec-demo not reachable -- retrying..."
  ensure_codec_demo
  if ! curl -sf http://localhost:8091/health >/dev/null 2>&1 ||
     ! kubectl rollout status deployment/codec-worker --timeout=2m; then
    echo "ERROR: codec-demo is still not reachable after retry." >&2
    exit 1
  fi
  echo "codec-demo healthy on retry!"
}

ensure_codec_demo
verify_codec_demo

echo ""
echo "codec-demo deployed and validated successfully!"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x _scripts/start-codec-demo.sh`

- [ ] **Step 3: Syntax-check it**

Run: `bash -n _scripts/start-codec-demo.sh`
Expected: no output, exit code 0.

- [ ] **Step 4: Commit**

```bash
git add _scripts/start-codec-demo.sh
git commit -m "feat: add standalone codec-demo deployment script"
```

---

### Task 2: Remove codec-demo from `_scripts/start.sh`

**Files:**
- Modify: `_scripts/start.sh`

**Interfaces:**
- Consumes: nothing new.
- Produces: `start.sh` with no references to codec-demo; codec-server/codec-worker are no longer deployed by the day-1 setup pass.

- [ ] **Step 1: Remove the `ensure_codec_demo` function**

In `_scripts/start.sh`, delete this entire block (currently directly above `ensure_temporal_namespace`):

```bash
# codec-demo is this repo's own code, not a published image, so it's built
# and loaded into the k3d cluster directly rather than pulled from a
# registry -- same reasoning as ensure_worker_control_ui.
ensure_codec_demo() {
  echo "Building codec-demo image..."
  docker build -t codec-demo:local ./codec-demo
  echo "Importing codec-demo image into the k3d cluster..."
  k3d image import codec-demo:local -c dev

  echo "Applying codec-demo manifests..."
  kubectl apply -f codec-demo/manifests.yaml

  # The image tag never changes across rebuilds, so `kubectl apply` alone
  # won't pick up new image content unless the pod spec itself changed --
  # force a fresh rollout so a rebuilt image is always picked up, same
  # reasoning as the forced restart in ensure_worker_control_ui.
  kubectl rollout restart deployment/codec-server deployment/codec-worker
  kubectl rollout status deployment/codec-server --timeout=2m
  kubectl rollout status deployment/codec-worker --timeout=2m
}

```

- [ ] **Step 2: Remove the `verify_codec_demo` function**

Delete this entire block (currently directly below `verify_worker_control_ui`, directly above `verify_prometheus_targets`):

```bash
verify_codec_demo() {
  echo "Verifying codec-demo is reachable..."

  # /health alone only proves codec-server is up -- also check codec-worker's
  # rollout, since it's a separate Deployment that /health can't see and the
  # demo workflows silently never make progress if it's down.
  if curl -sf http://localhost:8091/health >/dev/null 2>&1 &&
     kubectl rollout status deployment/codec-worker --timeout=2m; then
    echo "codec-demo is healthy!"
    return
  fi

  echo "codec-demo not reachable -- retrying..."
  ensure_codec_demo
  if ! curl -sf http://localhost:8091/health >/dev/null 2>&1 ||
     ! kubectl rollout status deployment/codec-worker --timeout=2m; then
    echo "ERROR: codec-demo is still not reachable after retry." >&2
    exit 1
  fi
  echo "codec-demo healthy on retry!"
}

```

- [ ] **Step 3: Remove the two call sites**

In the main install sequence, delete this line (currently right after `ensure_worker_control_ui`):

```bash
ensure_codec_demo
```

In the post-start validation block, delete this line (currently the last line, right after `verify_worker_control_ui`):

```bash
verify_codec_demo
```

- [ ] **Step 4: Syntax-check the result**

Run: `bash -n _scripts/start.sh`
Expected: no output, exit code 0.

- [ ] **Step 5: Confirm no remaining references**

Run: `grep -n "codec" _scripts/start.sh`
Expected: no output (no matches).

- [ ] **Step 6: Commit**

```bash
git add _scripts/start.sh
git commit -m "refactor: remove codec-demo deployment from start.sh"
```

---

### Task 3: Update README pointer to the new script

**Files:**
- Modify: `README.md` (existing "Codec Server" section, currently starting at the `## Codec Server` heading)

**Interfaces:**
- Consumes: script path `_scripts/start-codec-demo.sh` from Task 1.
- Produces: none (docs-only).

- [ ] **Step 1: Add the run-it-first note**

In `README.md`, find this existing paragraph (the first paragraph under `## Codec Server`):

```markdown
This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. A worker polls the `codec-demo` task queue in the `default` namespace; start either workflow below to generate encrypted history.
```

Replace it with (adds a run-it-first note, no day/schedule references):

````markdown
This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. A worker polls the `codec-demo` task queue in the `default` namespace; start either workflow below to generate encrypted history.

Codec-server/codec-worker aren't deployed automatically — when you're ready to explore this, run:

```bash
_scripts/start-codec-demo.sh
```

Once that completes, start either workflow below to generate encrypted history.
````

- [ ] **Step 2: Review the rendered section**

Run: `sed -n '/## Codec Server/,/^## /p' README.md`
Expected: the new note appears once, directly after the intro paragraph and before the "Start the onboarding demo" heading; no duplicated or dangling code fences.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: point to start-codec-demo.sh in the Codec Server section"
```
