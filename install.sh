#!/usr/bin/env bash
set -euo pipefail

# HydraQL CodeQL Installer (Unix/macOS/Linux)
# Installs GitHub CodeQL CLI broadly. If a native package is available (e.g., brew), uses it.
# Otherwise falls back to downloading the official release zip.

CODEQL_VERSION_DEFAULT="2.17.6"
CODEQL_VERSION="${CODEQL_VERSION:-$CODEQL_VERSION_DEFAULT}"
INSTALL_DIR="${INSTALL_DIR:-/opt/codeql}"
BIN_LINK="/usr/local/bin/codeql"

die() { echo "ERROR: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "Missing dependency: $1"; }

echo ">> Detecting platform..."
UNAME_S="$(uname -s 2>/dev/null || true)"
UNAME_M="$(uname -m 2>/dev/null || true)"

# Package managers
HAS_APT=0; HAS_YUM=0; HAS_DNF=0; HAS_BREW=0; HAS_PACMAN=0; HAS_ZYPPER=0
command -v apt >/dev/null 2>&1 && HAS_APT=1
command -v yum >/dev/null 2>&1 && HAS_YUM=1
command -v dnf >/dev/null 2>&1 && HAS_DNF=1
command -v brew >/dev/null 2>&1 && HAS_BREW=1
command -v pacman >/dev/null 2>&1 && HAS_PACMAN=1
command -v zypper >/dev/null 2>&1 && HAS_ZYPPER=1

install_via_brew() {
  echo ">> Installing CodeQL via Homebrew..."
  brew update
  brew install codeql || brew upgrade codeql
}

install_via_zip_linux() {
  need curl
  need unzip
  local url="https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/codeql-linux64.zip"
  echo ">> Downloading CodeQL ${CODEQL_VERSION} for Linux: $url"
  tmpzip="$(mktemp -t codeql.XXXXXX.zip)"
  curl -fsSL -o "$tmpzip" "$url"
  sudo mkdir -p "$INSTALL_DIR-$CODEQL_VERSION"
  sudo unzip -q "$tmpzip" -d /tmp/codeql-unpack
  sudo mv /tmp/codeql-unpack/codeql "$INSTALL_DIR-$CODEQL_VERSION"
  sudo rm -rf /tmp/codeql-unpack "$tmpzip"
  sudo ln -sfn "$INSTALL_DIR-$CODEQL_VERSION" "$INSTALL_DIR"
  sudo ln -sfn "$INSTALL_DIR/codeql" "$BIN_LINK"
  echo ">> Installed to $INSTALL_DIR-$CODEQL_VERSION and symlinked to $BIN_LINK"
}

install_via_zip_macos() {
  need curl
  need unzip
  local url="https://github.com/github/codeql-cli-binaries/releases/download/v${CODEQL_VERSION}/codeql-osx64.zip"
  echo ">> Downloading CodeQL ${CODEQL_VERSION} for macOS: $url"
  tmpzip="$(mktemp -t codeql.XXXXXX.zip)"
  curl -fsSL -o "$tmpzip" "$url"
  sudo mkdir -p "$INSTALL_DIR-$CODEQL_VERSION"
  sudo unzip -q "$tmpzip" -d /tmp/codeql-unpack
  sudo mv /tmp/codeql-unpack/codeql "$INSTALL_DIR-$CODEQL_VERSION"
  sudo rm -rf /tmp/codeql-unpack "$tmpzip"
  sudo ln -sfn "$INSTALL_DIR-$CODEQL_VERSION" "$INSTALL_DIR"
  sudo ln -sfn "$INSTALL_DIR/codeql" "$BIN_LINK"
  echo ">> Installed to $INSTALL_DIR-$CODEQL_VERSION and symlinked to $BIN_LINK"
}

# Main flow
case "$UNAME_S" in
  Darwin)
    echo ">> Platform: macOS"
    if [ "$HAS_BREW" -eq 1 ]; then
      install_via_brew
    else
      install_via_zip_macos
    fi
    ;;
  Linux)
    echo ">> Platform: Linux ($UNAME_M)"
    # Prefer zip because distro repos vary / may be outdated or unavailable.
    install_via_zip_linux
    ;;
  *)
    die "Unsupported OS: $UNAME_S"
    ;;
esac

echo ">> Verifying installation..."
codeql --version || die "CodeQL not found on PATH after install."
echo "✅ CodeQL installed successfully."