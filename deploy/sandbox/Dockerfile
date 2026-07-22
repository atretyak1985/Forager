FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash python3 python3-pip python3-venv nodejs npm git curl jq ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 agent
USER agent
WORKDIR /workspace
CMD ["sleep", "infinity"]
