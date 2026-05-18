# ── Leaf Dockerfile ──────────────────────────────────────────────
# Multi-stage build: compiles the leaf binary, then packages it
# into a minimal runtime image. Leaf connects outbound to Vine via
# gRPC — no inbound ports, no Docker access needed.
#
# Usage:
#   docker build -f deploy/docker/leaf.Dockerfile -t ivy-leaf:latest .
#

# ── Stage 1: Build ──────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and proto-generated code
COPY . .

# Build the leaf binary
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/leaf ./cmd/leaf

# ── Stage 2: Runtime ────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        grep \
        gawk \
        findutils \
        coreutils \
        procps \
        systemd \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user (matches the ivy-leaf system user from Ansible)
RUN groupadd --system ivy-leaf && useradd --system --gid ivy-leaf --no-create-home ivy-leaf

# Install binary
COPY --from=builder /bin/leaf /usr/local/bin/leaf

# Config and data directories
RUN mkdir -p /etc/ivy-leaf /var/lib/ivy-leaf /var/log/ivy-leaf && \
    chown -R ivy-leaf:ivy-leaf /etc/ivy-leaf /var/lib/ivy-leaf /var/log/ivy-leaf

# Default config (override via volume mount for local testing)
COPY configs/leaf.yaml /etc/ivy-leaf/config.yaml

WORKDIR /var/lib/ivy-leaf

USER ivy-leaf

ENTRYPOINT ["leaf"]
CMD ["-config", "/etc/ivy-leaf/config.yaml"]
