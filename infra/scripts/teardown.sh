#!/usr/bin/env bash
# =============================================================
# teardown.sh - Elimina toda la infraestructura del stack
# =============================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()  { echo -e "${BLUE}[INFO]${NC}  $1"; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

STACK_NAME="${STACK_NAME:-mcp-server}"
AWS_REGION="${AWS_REGION:-us-east-1}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -s|--stack)   STACK_NAME="$2"; shift 2 ;;
        -r|--region)  AWS_REGION="$2"; shift 2 ;;
        -h|--help)
            echo "Uso: $0 [-s STACK_NAME] [-r REGION]"
            echo "Elimina todos los recursos del stack CloudFormation."
            echo ""
            echo "ADVERTENCIA: Esto elimina la instancia EC2, Elastic IP y todos los recursos."
            exit 0
            ;;
        *) log_error "Opción desconocida: $1"; exit 1 ;;
    esac
done

echo -e "${RED}"
echo "  ╔══════════════════════════════════════════════════════╗"
echo "  ║   ADVERTENCIA: ELIMINACIÓN DE INFRAESTRUCTURA        ║"
echo "  ╚══════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo -e "${YELLOW}Stack a eliminar : ${STACK_NAME}${NC}"
echo -e "${YELLOW}Región           : ${AWS_REGION}${NC}"
echo ""
echo -e "${RED}Se eliminarán:${NC}"
echo "  • Instancia EC2"
echo "  • Elastic IP (se liberará)"
echo "  • Security Group"
echo "  • IAM Role e Instance Profile"
echo "  • TODOS los datos almacenados en la instancia"
echo ""

read -rp "Escribe 'ELIMINAR' para confirmar: " confirm
if [[ "$confirm" != "ELIMINAR" ]]; then
    echo "Operación cancelada."
    exit 0
fi

log_info "Verificando que el stack existe..."
aws cloudformation describe-stacks \
    --stack-name "$STACK_NAME" \
    --region "$AWS_REGION" &>/dev/null || {
    log_error "Stack '${STACK_NAME}' no encontrado en ${AWS_REGION}"
    exit 1
}

log_info "Eliminando stack '${STACK_NAME}'..."
aws cloudformation delete-stack \
    --stack-name "$STACK_NAME" \
    --region "$AWS_REGION"

log_info "Esperando a que se complete la eliminación..."
log_info "(Esto puede tardar varios minutos)"

aws cloudformation wait stack-delete-complete \
    --stack-name "$STACK_NAME" \
    --region "$AWS_REGION"

log_ok "Stack '${STACK_NAME}' eliminado correctamente"
