# Build stage
FROM golang:1.23 AS builder

WORKDIR /usr/src/codec-demo

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/codec-server ./cmd/codec-server
RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/worker ./cmd/worker

# Runtime stage
FROM scratch

COPY --from=builder /usr/local/bin/codec-server /usr/local/bin/codec-server
COPY --from=builder /usr/local/bin/worker /usr/local/bin/worker

EXPOSE 8091

# manifests.yaml overrides this command for the worker Deployment.
CMD ["/usr/local/bin/codec-server"]
