#!/usr/bin/env bash
# =============================================================
# ssh-connect.sh - Conecta via SSH a la instancia EC2
# =============================================================
set -euo pipefail

BLUE='\033[0;34m'; NC='\033[0m'
STACK_NAME="${STACK_NAME:-mcp-server}"
AWS_REGION="${AWS_REGION:-us-east-2}"
AWS_PROFILE="${AWS_PROFILE:-mcp-demo}"
KEY_FILE="${KEY_FILE:-$HOME/.ssh/key_pair_mcp_demo.pem}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -f|--key-file) KEY_FILE="$2"; shift 2 ;;
        -s|--stack)    STACK_NAME="$2"; shift 2 ;;
        -r|--region)   AWS_REGION="$2"; shift 2 ;;
        -p|--profile)  AWS_PROFILE="$2"; shift 2 ;;
        -h|--help)
            echo "Uso: $0 -f KEY_FILE [-s STACK] [-r REGION] [-p PROFILE]"
            echo "Conecta via SSH a la instancia EC2 del stack MCP."
            exit 0
            ;;
        *) echo "Opción desconocida: $1"; exit 1 ;;
    esac
done

[[ -f "$KEY_FILE" ]] || {
    echo "Error: No se encontró el archivo de clave: ${KEY_FILE}"
    echo "Usa -f <ruta-al-pem> o exporta KEY_FILE"
    exit 1
}

PUBLIC_IP=$(command aws --profile "$AWS_PROFILE" --region "$AWS_REGION" \
    cloudformation describe-stacks \
    --stack-name "$STACK_NAME" \
    --query "Stacks[0].Outputs[?OutputKey=='InstancePublicIP'].OutputValue" \
    --output text 2>/dev/null)

[[ -z "$PUBLIC_IP" ]] && {
    echo "Error: No se encontró la IP del stack '${STACK_NAME}' en ${AWS_REGION} (perfil: ${AWS_PROFILE})"
    exit 1
}

echo -e "${BLUE}Conectando a ec2-user@${PUBLIC_IP} (perfil: ${AWS_PROFILE})...${NC}"
exec ssh -i "$KEY_FILE" -o StrictHostKeyChecking=no "ec2-user@${PUBLIC_IP}"
