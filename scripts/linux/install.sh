
#!/bin/bash
# Sonic - Instalador Completo para Linux
# Baixa (se necessário) + instala o Sonic

set -e

# Cores
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# Parâmetros
VERSION="local"
NO_DOWNLOAD=false
FORCE=false

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --version) VERSION="$2"; shift ;;
        --no-download) NO_DOWNLOAD=true ;;
        --force) FORCE=true ;;
        *) echo "Uso: $0 [--version <versão>|local] [--no-download] [--force]"; exit 1 ;;
    esac
    shift
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="/usr/local/bin"

# Detectar arquitetura
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo -e "${RED}ERRO: Arquitetura $ARCH não suportada${NC}"; exit 1 ;;
esac
EXE_NAME="sonic-linux-$ARCH"
LOCAL_EXE="$REPO_ROOT/dist/$EXE_NAME"
TARGET_EXE="$INSTALL_DIR/sonic"

echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}      Sonic Instalador Completo${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# Verifica se é root
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}ERRO: Por favor, execute como root (sudo)${NC}"
    exit 1
fi

# Passo 1: Baixar ou usar binário local
if [ "$VERSION" = "local" ] || [ "$NO_DOWNLOAD" = true ]; then
    echo -e "${YELLOW}1. Usando binário local...${NC}"
    if [ ! -f "$LOCAL_EXE" ]; then
        echo -e "${RED}   ERRO: Binário não encontrado em: $LOCAL_EXE${NC}"
        echo -e "${YELLOW}   Execute './scripts/build-linux.sh' primeiro.${NC}"
        exit 1
    fi
    SOURCE_EXE="$LOCAL_EXE"
    echo -e "${GREEN}   OK: $LOCAL_EXE${NC}"
else
    echo -e "${YELLOW}1. Baixando versão $VERSION...${NC}"
    GITHUB_REPO="manassesbinga/sonic"
    if [ "$VERSION" = "latest" ]; then
        RELEASE_URL="https://api.github.com/repos/$GITHUB_REPO/releases/latest"
    else
        RELEASE_URL="https://api.github.com/repos/$GITHUB_REPO/releases/tags/$VERSION"
    fi
    
    TEMP_DIR=$(mktemp -d -t sonic-install-XXXXXX)
    trap 'rm -rf "$TEMP_DIR"' EXIT
    
    # Baixa release e encontra o asset
    RELEASE=$(curl -s "$RELEASE_URL")
    ASSET_URL=$(echo "$RELEASE" | tr ',' '\n' | grep -A5 -B5 "browser_download_url" | grep -o "https.*linux-$ARCH.*" | head -1)
    if [ -z "$ASSET_URL" ]; then
        echo -e "${RED}   ERRO: Nenhum arquivo encontrado para Linux $ARCH${NC}"
        exit 1
    fi
    
    TEMP_EXE="$TEMP_DIR/$EXE_NAME"
    echo -e "   Baixando: $(basename "$ASSET_URL")..."
    curl -L -o "$TEMP_EXE" "$ASSET_URL" -s
    chmod +x "$TEMP_EXE"
    SOURCE_EXE="$TEMP_EXE"
    echo -e "${GREEN}   OK${NC}"
fi

# Passo 2: Instalar
echo ""
echo -e "${YELLOW}2. Instalando...${NC}"
mkdir -p "$INSTALL_DIR"

if [ -f "$TARGET_EXE" ] && [ "$FORCE" = false ]; then
    echo -e "${RED}   O Sonic já está instalado. Use --force para sobrescrever.${NC}"
    exit 1
fi

cp "$SOURCE_EXE" "$TARGET_EXE"
chmod +x "$TARGET_EXE"
echo -e "${GREEN}   Binário copiado${NC}"

# Copia guia de uso
if [ -f "$REPO_ROOT/GUIA_DE_USO.txt" ]; then
    mkdir -p "/usr/local/share/sonic"
    cp "$REPO_ROOT/GUIA_DE_USO.txt" "/usr/local/share/sonic/"
    echo -e "${GREEN}   Guia de uso copiado${NC}"
fi

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}     Instalação concluída com sucesso!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "Pasta de instalação: $INSTALL_DIR"
echo -e "Arquivo executável: $TARGET_EXE"
echo ""
echo -e "${CYAN}Para começar:${NC}"
echo -e "1. Execute 'sonic --help' para ver os comandos"
echo -e "2. Execute 'sonic info' para verificar a instalação"
echo ""
