# ============================================================
# HydraQL — Multi-stage Go build
# Stage 1: compile the binary
# Stage 2: minimal runtime with CodeQL CLI
# ============================================================

# ---- builder ----
FROM golang:1.21 AS builder

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

ARG CODEQL_VERSION=2.17.6

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl unzip ca-certificates procps \
 && rm -rf /var/lib/apt/lists/*

# Install CodeQL CLI — arch-aware (amd64 or arm64)
RUN ARCH=$(dpkg --print-architecture) && \
    case "$ARCH" in \
      amd64) CODEQL_PKG="codeql-linux64.zip" ;; \
      arm64) CODEQL_PKG="codeql-linux-arm64.zip" ;; \
      *) echo "Unsupported architecture: $ARCH" && exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/codeql.zip \
      "https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/${CODEQL_PKG}" \
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
