# =========================
# Stage 1: Runtime builder
# =========================
FROM python:3.11-slim AS runtime

# Set build-time args so you can pin the CodeQL version easily.
# Example build: docker build --build-arg CODEQL_VERSION=2.17.6 -t hydraql:latest .
ARG CODEQL_VERSION=2.17.6

# Install minimal tooling needed to fetch/unpack CodeQL
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl unzip ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Download and install the CodeQL CLI (linux64)
# If you want another version, pass --build-arg CODEQL_VERSION=X.Y.Z on build.
RUN curl -fsSL -o /tmp/codeql.zip \
      "https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/codeql-linux64.zip" \
 && mkdir -p /opt/codeql \
 && unzip -q /tmp/codeql.zip -d /opt \
 && mv /opt/codeql /opt/codeql-${CODEQL_VERSION} \
 && ln -s /opt/codeql-${CODEQL_VERSION} /opt/codeql \
 && ln -sf /opt/codeql/codeql /usr/local/bin/codeql \
 && rm -f /tmp/codeql.zip

# Create non-root user
RUN useradd -m hydra && mkdir -p /app && chown -R hydra:hydra /app
USER hydra
WORKDIR /app

# Copy HydraQL script into the image
# Ensure your script file name matches here; if it's different, adjust COPY.
COPY --chown=hydra:hydra hydraql.py /app/hydraql.py

# Make it runnable as a CLI
RUN printf '%s\n' '#!/bin/sh' 'exec python3 /app/hydraql.py "$@"' > /usr/local/bin/hydraql \
 && chmod +x /usr/local/bin/hydraql

# Default entrypoint to the HydraQL CLI
ENTRYPOINT ["hydraql"]
# Show help by default (so running the container with no args prints usage)
CMD ["--help"]