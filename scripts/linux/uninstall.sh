
#!/bin/bash
# Sonic - Desinstalador para Linux
# Remove o Sonic completamente

set -e

# Cores
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}       Sonic Desinstalador${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# Verifica se é root
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}ERRO: Por favor, execute como root (sudo)${NC}"
    exit 1
fi

# Confirmação
read -p "Tem certeza que quer desinstalar o Sonic? (s/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Ss]$ ]]; then
    echo -e "${YELLOW}Desinstalação cancelada.${NC}"
    exit 0
fi

# Remover binário
echo -e "${YELLOW}1. Removendo binário...${NC}"
rm -f "/usr/local/bin/sonic"
echo -e "${GREEN}   OK${NC}"

# Remover guia de uso
echo -e "${YELLOW}2. Removendo guia de uso...${NC}"
rm -rf "/usr/local/share/sonic"
echo -e "${GREEN}   OK${NC}"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}   Desinstalação concluída com sucesso!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
