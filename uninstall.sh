#!/bin/bash
set -e

BINARY="sonic"
DEST="/usr/local/bin"

if [ "$EUID" -ne 0 ]; then
  echo "ERRO: Execute como root ou com sudo:"
  echo "  sudo ./uninstall.sh"
  exit 1
fi

rm -f "$DEST/$BINARY"
echo "✓ Sonic removido de: $DEST/$BINARY"
