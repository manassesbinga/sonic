#!/bin/bash
set -e

BINARY="sonic"
DEST="/usr/local/bin"

if [ "$EUID" -ne 0 ]; then
  echo "ERRO: Execute como root ou com sudo:"
  echo "  sudo ./install.sh"
  exit 1
fi

if [ -f "$BINARY" ]; then
  cp "$BINARY" "$DEST/"
elif [ -f "release/amd64/$BINARY" ]; then
  cp "release/amd64/$BINARY" "$DEST/"
else
  echo "Compilando $BINARY..."
  CGO_ENABLED=0 go build -ldflags="-s -w" -o "$DEST/$BINARY" main.go
  echo "✓ Compilado e instalado"
  exit 0
fi

chmod +x "$DEST/$BINARY"
echo ""
echo "✓ Sonic instalado em: $DEST/$BINARY"
echo ""
echo "Para testar:"
echo "  sonic --help"
echo "  sonic version"
echo ""
