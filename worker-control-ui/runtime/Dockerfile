# The upstream benchmark-workers image (ghcr.io/temporalio/benchmark-workers)
# is FROM scratch -- no shell, no `kill` -- so `kubectl exec` can't reach the
# worker/runner process inside it to signal it directly. This derived image
# copies the same statically-linked binaries onto a minimal busybox base
# instead, purely so worker-control-ui's "Kill" action (internal/k8s
# Manager.kill) can exec `kill -9 -1` (everything but PID 1 -- see
# wrappedCommand in manager.go for why not PID 1 itself) to crash the process
# in place without deleting the Pod, preserving its logs (`kubectl logs
# --previous`).
FROM ghcr.io/temporalio/benchmark-workers:latest AS bw

# omes (github.com/temporalio/omes) provides the Runner's rate-limited
# dispatch mode -- benchmark-workers' own runner has no rate-limiting flag at
# all. Pinned to a specific commit (omes has no tagged releases) rather than
# `main`, matching this repo's version-pinning discipline elsewhere. Requires
# Go 1.26+ per its go.mod; that only affects this isolated build stage, not
# the devcontainer's own pinned Go 1.23.3 toolchain.
FROM golang:1.26 AS omes-builder
ARG OMES_COMMIT=b56905e4ec6227d3c1f91fc92621192450b1ca91
WORKDIR /omes
RUN git init -q && \
    git remote add origin https://github.com/temporalio/omes.git && \
    git fetch --depth 1 origin "$OMES_COMMIT" && \
    git checkout -q FETCH_HEAD
# dispatch_hello_world.go (see omes-scenarios/) is dropped into omes' own
# scenarios/ package so it self-registers the same way every built-in
# scenario there does -- not a fork, just one file added at build time.
COPY omes-scenarios/*.go ./scenarios/
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -o /usr/local/bin/omes ./cmd/omes

# Rate mode's "Many Timers"/"Fan-Out" scenarios use omes' shared kitchen-sink
# workflow, which needs omes' own worker (benchmark-workers' worker doesn't
# know that workflow type). `run-scenario-with-worker` would otherwise
# live-compile that worker at every container start (it needs a `go` toolchain
# and this omes checkout present at runtime, neither of which the final
# busybox image has) -- `prepare-worker` builds it once, here, so runtime is
# just exec'ing an already-compiled binary. It must land at this exact path:
# the omes binary embeds its own build-time source location (via
# runtime.Caller) to find it, so the final image has to replicate
# /omes/workers/go/wcui exactly, not just carry the binary anywhere.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    /usr/local/bin/omes prepare-worker --language go --dir-name wcui

FROM busybox:1.36-musl

COPY --from=bw /usr/local/bin/worker /usr/local/bin/worker
COPY --from=bw /usr/local/bin/runner /usr/local/bin/runner
COPY --from=omes-builder /usr/local/bin/omes /usr/local/bin/omes
COPY --from=omes-builder /omes/workers/go/wcui /omes/workers/go/wcui
