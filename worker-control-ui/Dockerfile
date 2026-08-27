# Build stage
FROM golang:1.25 AS builder

WORKDIR /usr/src/worker-control-ui

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/worker-control-ui ./cmd/server

# Runtime stage
FROM scratch

COPY --from=builder /usr/local/bin/worker-control-ui /usr/local/bin/worker-control-ui

EXPOSE 8090

CMD ["/usr/local/bin/worker-control-ui"]
