#!/usr/bin/env bash
# =============================================================================
# ssh-connect.sh — Conecta vía SSH al Bot o al MCP Server
# =============================================================================
set -euo pipefail

BLUE='\033[0;34m'; NC='\033[0m'

STACK_NAME="${STACK_NAME:-mcp-deploy}"
AWS_REGION="${AWS_REGION:-us-east-2}"
AWS_PROFILE="${AWS_PROFILE:-mcp-deploy}"
KEY_FILE="${KEY_FILE:-$HOME/.ssh/key_pair_mcp_deploy.pem}"
TARGET="${TARGET:-bot}"  # bot | mcp

while [[ $# -gt 0 ]]; do
    case $1 in
        -f|--key-file) KEY_FILE="$2"; shift 2 ;;
        -s|--stack)    STACK_NAME="$2"; shift 2 ;;
        -r|--region)   AWS_REGION="$2"; shift 2 ;;
        -p|--profile)  AWS_PROFILE="$2"; shift 2 ;;
        --bot)         TARGET="bot"; shift ;;
        --mcp)         TARGET="mcp"; shift ;;
        -h|--help)
            echo "Uso: $0 [--bot|--mcp] -f KEY_FILE [-s STACK] [-r REGION] [-p PROFILE]"
            echo ""
            echo "  --bot   Conectar al Bot Telegram (default)"
            echo "  --mcp   Conectar al MCP Server"
            exit 0 ;;
        *) echo "Opción desconocida: $1"; exit 1 ;;
    esac
done

[[ -f "$KEY_FILE" ]] || {
    echo "Error: Archivo de clave no encontrado: ${KEY_FILE}"
    echo "Usa -f <ruta-al-pem>"
    exit 1
}

aws_cmd() { command aws --profile "$AWS_PROFILE" --region "$AWS_REGION" "$@"; }

get_cf_output() {
    aws_cmd cloudformation describe-stacks \
        --stack-name "$STACK_NAME" \
        --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" \
        --output text 2>/dev/null || echo ""
}

if [[ "$TARGET" == "bot" ]]; then
    PUBLIC_IP=$(get_cf_output "BotPublicIP")
    LABEL="Bot Telegram"
else
    PUBLIC_IP=$(aws_cmd ec2 describe-instances \
        --filters "Name=tag:Name,Values=${STACK_NAME}-mcp-server" \
                  "Name=instance-state-name,Values=running" \
        --query "Reservations[0].Instances[0].PublicIpAddress" \
        --output text 2>/dev/null || echo "")
    LABEL="MCP Server"
fi

[[ -z "$PUBLIC_IP" || "$PUBLIC_IP" == "None" ]] && {
    echo "Error: No se encontró la IP de '${LABEL}' en el stack '${STACK_NAME}'"
    exit 1
}

echo -e "${BLUE}Conectando a ${LABEL} — ec2-user@${PUBLIC_IP}${NC}"
exec ssh -i "$KEY_FILE" -o StrictHostKeyChecking=no "ec2-user@${PUBLIC_IP}"
