#!/usr/bin/env bash
# =============================================================
# ssh-connect.sh - Conecta via SSH a la instancia EC2
# =============================================================
set -euo pipefail

BLUE='\033[0;34m'; NC='\033[0m'
STACK_NAME="${STACK_NAME:-mcp-server}"
AWS_REGION="${AWS_REGION:-us-east-1}"
KEY_FILE="${KEY_FILE:-}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -f|--key-file) KEY_FILE="$2"; shift 2 ;;
        -s|--stack)    STACK_NAME="$2"; shift 2 ;;
        -r|--region)   AWS_REGION="$2"; shift 2 ;;
        -h|--help)
            echo "Uso: $0 -f KEY_FILE [-s STACK] [-r REGION]"
            echo "Conecta via SSH a la instancia EC2 del stack MCP."
            exit 0
            ;;
        *) echo "Opción desconocida: $1"; exit 1 ;;
    esac
done

[[ -z "$KEY_FILE" ]] && {
    echo "Error: KEY_FILE requerido. Usa -f <ruta-al-pem> o exporta KEY_FILE"
    exit 1
}

PUBLIC_IP=$(aws cloudformation describe-stacks \
    --stack-name "$STACK_NAME" \
    --region "$AWS_REGION" \
    --query "Stacks[0].Outputs[?OutputKey=='InstancePublicIP'].OutputValue" \
    --output text 2>/dev/null)

[[ -z "$PUBLIC_IP" ]] && {
    echo "Error: No se encontró la IP del stack '${STACK_NAME}' en ${AWS_REGION}"
    exit 1
}

echo -e "${BLUE}Conectando a ec2-user@${PUBLIC_IP}...${NC}"
exec ssh -i "$KEY_FILE" -o StrictHostKeyChecking=no "ec2-user@${PUBLIC_IP}"
