# Decouple codec-demo Deployment — Design

## Goal

The codec-demo (codec-server + codec-worker) currently deploys automatically as part of `_scripts/start.sh`'s single pass, alongside every other service. It's used later in the workshop than the rest of the stack, so it shouldn't have to build/import/roll out on every codespace creation. Move its deployment behind a separate, manually-run script instead.

## Changes

### `_scripts/start.sh`

Remove entirely:
- The `ensure_codec_demo` function definition.
- The `verify_codec_demo` function definition.
- Both call sites (`ensure_codec_demo` in the main install sequence, `verify_codec_demo` in post-start validation).

Nothing else in `start.sh` changes — codec-demo has no shared state (env vars, namespaces, DB grants) with the rest of the script, so removal is a clean cut.

### `_scripts/start-codec-demo.sh` (new)

A self-contained script, not sourced from or sourcing `start.sh`. Carries its own copies of the same two functions (renamed to drop the now-meaningless "ensure"/"verify" split from the shared-file convention, but keeping the same ensure/verify *behavior*):

- Build the `codec-demo:local` image, `k3d image import` it into the `dev` cluster.
- `kubectl apply -f codec-demo/manifests.yaml`, then force a rollout restart of both `codec-server` and `codec-worker` Deployments (needed because the image tag never changes across rebuilds).
- Wait for both rollouts (`kubectl rollout status`, 2m timeout each).
- Verify: curl `http://localhost:8091/health` and check the `codec-worker` rollout. On failure, retry the whole ensure step once; if still failing, print an error and `exit 1`.
- On success, print a confirmation message.

Assumes the `dev` k3d cluster already exists (created by `start.sh`). No explicit existence check — consistent with the rest of the repo's scripts, which already assume prior setup steps ran.

### `README.md`

Add a short note at the top of the existing "Codec Server" section pointing readers to run `_scripts/start-codec-demo.sh` before using the section's examples. Framed as "when you're ready to explore this" rather than tied to a specific day or schedule, since the workshop's structure and content may change independently of this deployment split.

## Out of scope

- No changes to `codec-demo/manifests.yaml`, the Dockerfile, or any Go source.
- No changes to `.devcontainer/devcontainer.json` — port 8091 stays forwarded/public regardless of whether codec-demo is currently deployed.
- No teardown/undeploy script — out of scope unless requested.

## Testing

No automated test suite covers `start.sh` today (it's an imperative provisioning script). Validate by reading the extracted script for correctness and, if a Codespace is available, running it against an already-running `dev` cluster to confirm codec-server/codec-worker come up and `verify` passes.
