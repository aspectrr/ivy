FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        python3 \
        python3-pip \
        curl \
        jq \
        grep \
        gawk \
        findutils \
        coreutils \
        procps \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /workspace
WORKDIR /workspace

# Keep container alive for sandbox lifetime.
CMD ["sleep", "infinity"]
