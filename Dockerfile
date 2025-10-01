# Multi-stage: Python + CodeQL CLI
FROM python:3.11-slim AS base

ARG CODEQL_VERSION=2.17.6

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl unzip ca-certificates procps \
 && rm -rf /var/lib/apt/lists/*

# Install CodeQL CLI (linux64)
RUN curl -fsSL -o /tmp/codeql.zip \
      "https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/codeql-linux64.zip" \
 && mkdir -p /opt/codeql \
 && unzip -q /tmp/codeql.zip -d /opt \
 && mv /opt/codeql /opt/codeql-${CODEQL_VERSION} \
 && ln -s /opt/codeql-${CODEQL_VERSION} /opt/codeql \
 && ln -sf /opt/codeql/codeql /usr/local/bin/codeql \
 && rm -f /tmp/codeql.zip

# Non-root user
RUN useradd -m hydra && mkdir -p /app && chown -R hydra:hydra /app
USER hydra
WORKDIR /app

# Copy HydraQL
COPY --chown=hydra:hydra hydraql.py /app/hydraql.py

# Small CLI wrapper
RUN printf '%s\n' '#!/bin/sh' 'exec python3 /app/hydraql.py "$@"' > /usr/local/bin/hydraql \
 && chmod +x /usr/local/bin/hydraql

ENTRYPOINT ["hydraql"]
CMD ["--help"]