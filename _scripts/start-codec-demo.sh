#! /bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

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

# kubectl rollout status alone can't tell us the worker actually connected --
# codec-worker has no readinessProbe and worker/main.go retries client.Dial
# forever instead of exiting, so the pod reports Ready the instant it starts
# regardless of whether it ever reached the Temporal frontend. Grep for the
# log line main.go only prints after a successful Dial + worker start.
codec_worker_connected() {
  kubectl rollout status deployment/codec-worker --timeout=2m &&
  kubectl logs deployment/codec-worker --tail=20 2>/dev/null | grep -q "starting worker on task queue"
}

verify_codec_demo() {
  echo "Verifying codec-demo is reachable..."

  # /health alone only proves codec-server is up -- also check codec-worker
  # actually connected, since it's a separate Deployment that /health can't
  # see and the demo workflows silently never make progress if it's down.
  if curl -sf http://localhost:8091/health >/dev/null 2>&1 &&
     codec_worker_connected; then
    echo "codec-demo is healthy!"
    return
  fi

  echo "codec-demo not reachable -- retrying..."
  ensure_codec_demo
  if ! curl -sf http://localhost:8091/health >/dev/null 2>&1 ||
     ! codec_worker_connected; then
    echo "ERROR: codec-demo is still not reachable after retry." >&2
    exit 1
  fi
  echo "codec-demo healthy on retry!"
}

ensure_codec_demo
verify_codec_demo

echo ""
echo "codec-demo deployed and validated successfully!"
