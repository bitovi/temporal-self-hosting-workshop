FROM mcr.microsoft.com/devcontainers/base:ubuntu

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg tar \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/install

RUN mkdir -p /workspaces/.temporal \
    && chmod 777 /workspaces/.temporal

# Install Go
ARG GO_VERSION=1.23.3
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local \
    && ln -s /usr/local/go/bin/go /usr/local/bin/go \
    && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"
ENV PATH="${GOPATH}/bin:${PATH}"

# Install docker-compose (works with Podman via podman-docker)
RUN curl -fsSL "https://github.com/docker/compose/releases/download/v2.24.5/docker-compose-linux-${TARGETARCH}" \
    -o /usr/local/bin/docker-compose \
 && chmod +x /usr/local/bin/docker-compose

# Install Temporal CLI
RUN curl -fsSL "https://temporal.download/cli/archive/latest?platform=linux&arch=${TARGETARCH}" \
    | tar -xz -C /usr/local/bin temporal \
    && chmod +x /usr/local/bin/temporal