#!/usr/bin/env bash
# =============================================================================
# teardown.sh — Elimina toda la infraestructura del stack MCP Deploy
# =============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()  { echo -e "${BLUE}[INFO]${NC}  $1"; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

STACK_NAME="${STACK_NAME:-mcp-deploy}"
AWS_REGION="${AWS_REGION:-us-east-2}"
AWS_PROFILE="${AWS_PROFILE:-mcp-deploy}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -s|--stack)   STACK_NAME="$2"; shift 2 ;;
        -r|--region)  AWS_REGION="$2"; shift 2 ;;
        -p|--profile) AWS_PROFILE="$2"; shift 2 ;;
        -h|--help)
            echo "Uso: $0 [-s STACK_NAME] [-r REGION] [-p PROFILE]"
            echo "Elimina todos los recursos del stack CloudFormation."
            exit 0 ;;
        *) log_error "Opción desconocida: $1"; exit 1 ;;
    esac
done

aws() { command aws --profile "$AWS_PROFILE" --region "$AWS_REGION" "$@"; }

echo -e "${RED}"
echo "  ╔══════════════════════════════════════════════════════╗"
echo "  ║   ADVERTENCIA: ELIMINACIÓN DE INFRAESTRUCTURA        ║"
echo "  ╚══════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo -e "${YELLOW}Stack    : ${STACK_NAME}${NC}"
echo -e "${YELLOW}Región   : ${AWS_REGION}${NC}"
echo -e "${YELLOW}Perfil   : ${AWS_PROFILE}${NC}"
echo ""
echo -e "${RED}Se eliminarán:${NC}"
echo "  • Instancia EC2 Bot + Instancia EC2 MCP Server"
echo "  • Elastic IP del Bot (se liberará — deja de funcionar el bot)"
echo "  • VPC, Subnet, Security Groups"
echo "  • IAM Role e Instance Profile"
echo ""

read -rp "Escribe 'ELIMINAR' para confirmar: " confirm
if [[ "$confirm" != "ELIMINAR" ]]; then
    echo "Operación cancelada."
    exit 0
fi

log_info "Verificando stack..."
aws cloudformation describe-stacks --stack-name "$STACK_NAME" &>/dev/null || {
    log_error "Stack '${STACK_NAME}' no encontrado en ${AWS_REGION} (perfil: ${AWS_PROFILE})"
    exit 1
}

log_info "Eliminando stack '${STACK_NAME}'..."
aws cloudformation delete-stack --stack-name "$STACK_NAME"

log_info "Esperando eliminación (puede tardar varios minutos)..."
aws cloudformation wait stack-delete-complete --stack-name "$STACK_NAME"

log_ok "Stack '${STACK_NAME}' eliminado correctamente"
