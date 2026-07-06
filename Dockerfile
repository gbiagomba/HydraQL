# ============================================================
# HydraQL — Multi-stage Go build
# Stage 1: compile the binary
# Stage 2: minimal runtime with CodeQL CLI
# ============================================================

# ---- builder ----
FROM golang:1.26-bookworm AS builder

ARG VERSION=dev
WORKDIR /src

# Cache dependency layer separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /hydraql \
      ./cmd/hydraql

# ---- final ----
FROM debian:bookworm-slim AS final

ARG CODEQL_VERSION=2.25.6

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl unzip ca-certificates procps \
 && rm -rf /var/lib/apt/lists/*

# CodeQL CLI only ships a linux64 (x86_64) binary — no arm64 Linux package exists
RUN curl -fsSL -o /tmp/codeql.zip \
      "https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/codeql-linux64.zip" \
 && mkdir -p /opt \
 && unzip -q /tmp/codeql.zip -d /opt \
 && mv /opt/codeql /opt/codeql-${CODEQL_VERSION} \
 && ln -s /opt/codeql-${CODEQL_VERSION} /opt/codeql \
 && ln -sf /opt/codeql/codeql /usr/local/bin/codeql \
 && rm -f /tmp/codeql.zip

# Non-root user
RUN useradd -m hydra && mkdir -p /app && chown -R hydra:hydra /app

# Copy compiled binary from builder stage
COPY --from=builder /hydraql /usr/local/bin/hydraql
RUN chmod +x /usr/local/bin/hydraql

USER hydra
WORKDIR /app

ENTRYPOINT ["hydraql"]
CMD ["--help"]
