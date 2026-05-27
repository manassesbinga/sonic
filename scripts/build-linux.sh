
#!/bin/bash
# Sonic - Build Script para Linux
# Compila o Sonic para Linux e gera o binário otimizado

set -e

# Cores
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# Parâmetros
RELEASE=false
NO_INSTALL=false

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --release) RELEASE=true ;;
        --no-install) NO_INSTALL=true ;;
        *) echo "Uso: $0 [--release] [--no-install]"; exit 1 ;;
    esac
    shift
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="$REPO_ROOT/dist"

# Detectar arquitetura
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo -e "${RED}ERRO: Arquitetura $ARCH não suportada${NC}"; exit 1 ;;
esac
EXE_NAME="sonic-linux-$ARCH"
OUTPUT_PATH="$OUTPUT_DIR/$EXE_NAME"

echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}         Sonic Build (Linux)${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# Cria pasta dist se não existir
mkdir -p "$OUTPUT_DIR"

# Flags de compilação
LDFLAGS=""
if [ "$RELEASE" = true ]; then
    LDFLAGS="-ldflags='-s -w'"
fi

echo -e "${YELLOW}1. Compilando Sonic...${NC}"
cd "$REPO_ROOT"
CMD="go build $LDFLAGS -o \"$OUTPUT_PATH\" ."
echo -e "   Executando: $CMD"
eval "$CMD"

# Obtém tamanho do arquivo
FILE_SIZE=$(du -h "$OUTPUT_PATH" | cut -f1)
echo -e "${GREEN}   OK!${NC}"

echo ""
echo -e "${YELLOW}2. Binário gerado:${NC}"
echo -e "   Caminho: $OUTPUT_PATH"
echo -e "   Tamanho: $FILE_SIZE"

# Instalação automática, se solicitado
if [ "$NO_INSTALL" = false ]; then
    echo ""
    echo -e "${YELLOW}3. Iniciando instalação...${NC}"
    if [ -f "$SCRIPT_DIR/linux/install.sh" ]; then
        chmod +x "$SCRIPT_DIR/linux/install.sh"
        sudo "$SCRIPT_DIR/linux/install.sh"
    else
        echo -e "${YELLOW}   Aviso: Instalador não encontrado${NC}"
    fi
fi

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}          Build concluído!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
