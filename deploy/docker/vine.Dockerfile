# ── Vine Dockerfile ──────────────────────────────────────────────
# Multi-stage build: compiles the vine binary, then packages it
# into a minimal runtime image with Docker CLI for sibling container
# management (agent sandboxes + pipeline sandboxes via docker.sock).
#
# Usage:
#   docker build -f deploy/docker/vine.Dockerfile -t ivy-vine:latest .
#

# ── Stage 1: Build ──────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and proto-generated code
COPY . .

# Build the vine binary
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/vine ./cmd/vine

# ── Stage 2: Runtime ────────────────────────────────────────────
FROM debian:bookworm-slim

# Install Docker CLI (for sibling container management via mounted docker.sock),
# plus ca-certificates for HTTPS/TLS connections to LLM APIs and ClickUp.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && chmod a+r /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for the daemon
RUN groupadd --system ivy && useradd --system --gid ivy --no-create-home ivy

# Install binary
COPY --from=builder /bin/vine /usr/local/bin/vine

# Config and data directories
RUN mkdir -p /etc/ivy /var/lib/ivy /var/log/ivy && chown -R ivy:ivy /etc/ivy /var/lib/ivy /var/log/ivy

# Copy default config (can be overridden via volume mount)
COPY configs/vine.yaml /etc/ivy/config.yaml

WORKDIR /var/lib/ivy

EXPOSE 50051 8080

USER ivy

ENTRYPOINT ["vine"]
CMD ["-config", "/etc/ivy/config.yaml"]
