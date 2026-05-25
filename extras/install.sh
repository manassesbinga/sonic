#!/bin/sh
# Sonic — Quick Install Script
# Usage: curl -fsSL https://github.com/anomalyco/sonic/raw/main/extras/install.sh | sh

set -e

BINARY="sonic"
VERSION="${1:-latest}"
INSTALL_DIR="/usr/local/bin"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux|darwin) ;;
    *) echo "Unsupported OS: $OS (try Docker instead)"; exit 1 ;;
esac

# Build URL
if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/anomalyco/sonic/releases/latest/download/${BINARY}-${OS}-${ARCH}"
else
    URL="https://github.com/anomalyco/sonic/releases/download/${VERSION}/${BINARY}-${OS}-${ARCH}"
fi

echo "Downloading Sonic for $OS/$ARCH..."
if command -v curl > /dev/null 2>&1; then
    curl -fsSL "$URL" -o "$INSTALL_DIR/$BINARY"
elif command -v wget > /dev/null 2>&1; then
    wget -q "$URL" -O "$INSTALL_DIR/$BINARY"
else
    echo "Need curl or wget"
    exit 1
fi

chmod +x "$INSTALL_DIR/$BINARY"
echo "Installed $INSTALL_DIR/$BINARY"
echo ""
echo "Run: sonic init && sonic dev"
