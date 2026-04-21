#!/usr/bin/env bash
# =============================================================
# deploy.sh - Despliega infraestructura y aplicación MCP en AWS
# Binarios Go se compilan localmente (cross-compile arm64) y se
# copian al EC2. No se necesita Python ni Go en el servidor.
# =============================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC}  $1"; }
log_ok()      { echo -e "${GREEN}[OK]${NC}    $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC}  $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
log_section() { echo -e "\n${BLUE}━━━ $1 ━━━${NC}"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CFN_TEMPLATE="${ROOT_DIR}/infra/cloudformation/stack.yml"

STACK_NAME="${STACK_NAME:-mcp-server}"
AWS_REGION="${AWS_REGION:-us-east-2}"
AWS_PROFILE="${AWS_PROFILE:-mcp-demo}"
INSTANCE_TYPE="${INSTANCE_TYPE:-t4g.micro}"
SSH_CIDR="${SSH_CIDR:-0.0.0.0/0}"
KEY_PAIR_NAME="${KEY_PAIR_NAME:-}"
KEY_FILE="${KEY_FILE:-$HOME/.ssh/key_pair_mcp_demo.pem}"
INFRA_ONLY=false
APP_ONLY=false
SKIP_CONFIRM=false

# ==============================================================
# Ayuda
# ==============================================================
show_usage() {
    cat <<EOF
Uso: $0 [OPCIONES]

Opciones:
  -k, --key-pair NAME     Nombre del Key Pair EC2 en AWS (requerido para infra)
  -f, --key-file  PATH    Ruta al archivo .pem para SSH (requerido para app)
  -s, --stack     NAME    Nombre del stack CloudFormation (default: mcp-server)
  -r, --region    REGION  Región AWS (default: us-east-2)
  -p, --profile   PROFILE Perfil AWS CLI (default: mcp-demo)
  -t, --type      TYPE    Tipo de instancia (default: t4g.micro)
  --cidr          CIDR    CIDR permitido para SSH (default: 0.0.0.0/0)
  --infra-only            Solo desplegar infraestructura
  --app-only              Solo desplegar aplicación (infra debe existir)
  -y, --yes               No pedir confirmación
  -h, --help              Mostrar esta ayuda

Variables de entorno equivalentes:
  STACK_NAME, AWS_REGION, AWS_PROFILE, INSTANCE_TYPE, KEY_PAIR_NAME, KEY_FILE, SSH_CIDR

Ejemplo rápido:
  export KEY_PAIR_NAME=mi-key-pair
  export KEY_FILE=~/.ssh/mi-key.pem
  $0

Ejemplo completo:
  $0 -k mi-key -f ~/.ssh/mi-key.pem -r us-east-2 -p mcp-demo -t t4g.micro
EOF
}

# ==============================================================
# Parseo de argumentos
# ==============================================================
while [[ $# -gt 0 ]]; do
    case $1 in
        -k|--key-pair)  KEY_PAIR_NAME="$2"; shift 2 ;;
        -f|--key-file)  KEY_FILE="$2"; shift 2 ;;
        -s|--stack)     STACK_NAME="$2"; shift 2 ;;
        -r|--region)    AWS_REGION="$2"; shift 2 ;;
        -p|--profile)   AWS_PROFILE="$2"; shift 2 ;;
        -t|--type)      INSTANCE_TYPE="$2"; shift 2 ;;
        --cidr)         SSH_CIDR="$2"; shift 2 ;;
        --infra-only)   INFRA_ONLY=true; shift ;;
        --app-only)     APP_ONLY=true; shift ;;
        -y|--yes)       SKIP_CONFIRM=true; shift ;;
        -h|--help)      show_usage; exit 0 ;;
        *) log_error "Opción desconocida: $1"; show_usage; exit 1 ;;
    esac
done

# Helper que inyecta perfil y región a cualquier llamada aws
aws() { command aws --profile "$AWS_PROFILE" --region "$AWS_REGION" "$@"; }

# ==============================================================
# Prerrequisitos
# ==============================================================
check_prerequisites() {
    log_section "Verificando prerrequisitos"

    local missing=false

    command -v aws  &>/dev/null || { log_error "AWS CLI no encontrado. Instala: https://aws.amazon.com/cli/"; missing=true; }
    command -v go   &>/dev/null || { log_error "Go no encontrado. Instala: https://go.dev/dl/"; missing=true; }
    command -v ssh  &>/dev/null || { log_error "ssh no encontrado"; missing=true; }
    command -v scp  &>/dev/null || { log_error "scp no encontrado"; missing=true; }

    [[ "$missing" == "true" ]] && exit 1

    local identity
    identity=$(aws sts get-caller-identity 2>&1) || {
        log_error "Credenciales AWS no válidas para el perfil '${AWS_PROFILE}'."
        log_warn "Verifica con: aws --profile ${AWS_PROFILE} sts get-caller-identity"
        exit 1
    }
    local account
    account=$(echo "$identity" | grep -o '"Account": "[^"]*"' | cut -d'"' -f4 || echo "unknown")
    log_ok "AWS autenticado — perfil: ${AWS_PROFILE} | cuenta: ${account} | región: ${AWS_REGION}"
    log_ok "Go disponible: $(go version)"
}

# ==============================================================
# Compilar binarios Go para linux/arm64
# ==============================================================
build_binaries() {
    log_section "Compilando binarios Go (linux/arm64)"

    mkdir -p "${ROOT_DIR}/bin"

    log_info "Compilando telegram-bot..."
    GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" \
        -o "${ROOT_DIR}/bin/telegram-bot" "${ROOT_DIR}/cmd/bot/"

    log_info "Compilando mcp-server..."
    GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" \
        -o "${ROOT_DIR}/bin/mcp-server" "${ROOT_DIR}/cmd/mcp-server/"

    log_ok "Binarios compilados:"
    ls -lh "${ROOT_DIR}/bin/"
}

# ==============================================================
# Desplegar CloudFormation
# ==============================================================
deploy_infrastructure() {
    log_section "Desplegando infraestructura CloudFormation"

    [[ -z "$KEY_PAIR_NAME" ]] && {
        log_error "KEY_PAIR_NAME requerido. Usa -k o exporta la variable."
        log_warn "Para crear un key pair:"
        log_warn "  aws --profile ${AWS_PROFILE} ec2 create-key-pair --key-name mi-key --query 'KeyMaterial' --output text > mi-key.pem"
        exit 1
    }

    [[ -f "$CFN_TEMPLATE" ]] || { log_error "Template no encontrado: ${CFN_TEMPLATE}"; exit 1; }

    log_info "Stack    : ${STACK_NAME}"
    log_info "Región   : ${AWS_REGION}"
    log_info "Perfil   : ${AWS_PROFILE}"
    log_info "Instancia: ${INSTANCE_TYPE}"
    log_info "Key Pair : ${KEY_PAIR_NAME}"
    log_info "SSH CIDR : ${SSH_CIDR}"

    if [[ "$SKIP_CONFIRM" == "false" ]]; then
        echo ""
        read -rp "¿Continuar con el despliegue? [y/N] " confirm
        [[ "$confirm" =~ ^[Yy]$ ]] || { echo "Cancelado."; exit 0; }
    fi

    aws cloudformation deploy \
        --template-file "$CFN_TEMPLATE" \
        --stack-name "$STACK_NAME" \
        --parameter-overrides \
            "KeyPairName=${KEY_PAIR_NAME}" \
            "InstanceType=${INSTANCE_TYPE}" \
            "SSHAllowedCidr=${SSH_CIDR}" \
            "ProjectName=${STACK_NAME}" \
        --capabilities CAPABILITY_NAMED_IAM \
        --no-fail-on-empty-changeset

    log_ok "Infraestructura desplegada"
}

# ==============================================================
# Helpers CloudFormation
# ==============================================================
get_output() {
    aws cloudformation describe-stacks \
        --stack-name "$STACK_NAME" \
        --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" \
        --output text 2>/dev/null || echo ""
}

# ==============================================================
# Esperar SSH
# ==============================================================
wait_for_ssh() {
    local ip="$1"
    local max_attempts=36  # 6 minutos
    local attempt=0

    log_info "Esperando SSH en ${ip}..."
    while (( attempt < max_attempts )); do
        if ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=no \
               -o BatchMode=yes -i "$KEY_FILE" \
               "ec2-user@${ip}" true 2>/dev/null; then
            echo ""
            log_ok "SSH disponible"
            return 0
        fi
        (( attempt++ ))
        echo -n "."
        sleep 10
    done
    echo ""
    log_error "SSH no disponible después de $((max_attempts * 10)) segundos"
    exit 1
}

# ==============================================================
# Desplegar aplicación
# ==============================================================
deploy_application() {
    log_section "Desplegando aplicación"

    [[ -z "$KEY_FILE" ]] && {
        log_error "KEY_FILE requerido. Usa -f o exporta la variable."
        exit 1
    }
    [[ -f "$KEY_FILE" ]] || { log_error "Archivo .pem no encontrado: ${KEY_FILE}"; exit 1; }

    local PUBLIC_IP
    PUBLIC_IP=$(get_output "InstancePublicIP")
    [[ -z "$PUBLIC_IP" ]] && {
        log_error "No se pudo obtener la IP del stack '${STACK_NAME}'"
        log_warn "¿Está desplegada la infraestructura? Ejecuta sin --app-only primero."
        exit 1
    }
    log_info "IP de la instancia: ${PUBLIC_IP}"
    chmod 400 "$KEY_FILE"

    # Verificar/crear .env
    if [[ ! -f "${ROOT_DIR}/.env" ]]; then
        if [[ -f "${ROOT_DIR}/.env.example" ]]; then
            log_warn ".env no encontrado. Copiando desde .env.example..."
            cp "${ROOT_DIR}/.env.example" "${ROOT_DIR}/.env"
            log_warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            log_warn "IMPORTANTE: Edita ${ROOT_DIR}/.env antes de continuar"
            log_warn "  TELEGRAM_BOT_TOKEN — obtenlo de @BotFather en Telegram"
            log_warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            read -rp "Presiona Enter después de editar .env (Ctrl+C para cancelar)..."
        else
            log_error ".env.example no encontrado en ${ROOT_DIR}"
            exit 1
        fi
    fi

    # Compilar binarios
    build_binaries

    wait_for_ssh "$PUBLIC_IP"

    local SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=30 -i ${KEY_FILE}"
    local SCP_OPTS="-o StrictHostKeyChecking=no -i ${KEY_FILE}"

    log_info "Preparando directorios remotos..."
    ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" \
        "sudo mkdir -p /opt/mcp/app /opt/mcp/bin /opt/mcp/files /opt/mcp/logs && \
         sudo chown -R ec2-user:ec2-user /opt/mcp"

    log_info "Copiando binarios Go..."
    scp $SCP_OPTS "${ROOT_DIR}/bin/telegram-bot" "ec2-user@${PUBLIC_IP}:/opt/mcp/bin/"
    scp $SCP_OPTS "${ROOT_DIR}/bin/mcp-server"   "ec2-user@${PUBLIC_IP}:/opt/mcp/bin/"
    ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" "chmod +x /opt/mcp/bin/*"

    log_info "Copiando configuración..."
    scp $SCP_OPTS "${ROOT_DIR}/.env"         "ec2-user@${PUBLIC_IP}:/opt/mcp/app/.env"
    scp $SCP_OPTS -r "${ROOT_DIR}/systemd/"  "ec2-user@${PUBLIC_IP}:/tmp/systemd_units/"

    # Copiar credenciales de Claude Code (OAuth)
    if [[ -f "${HOME}/.claude/.claude.json" ]]; then
        log_info "Copiando credenciales de Claude Code..."
        ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" "mkdir -p ~/.claude"
        scp $SCP_OPTS "${HOME}/.claude/.claude.json" "ec2-user@${PUBLIC_IP}:~/.claude/"
        [[ -f "${HOME}/.claude/settings.json" ]] && \
            scp $SCP_OPTS "${HOME}/.claude/settings.json" "ec2-user@${PUBLIC_IP}:~/.claude/"
        log_ok "Credenciales copiadas"
    else
        log_warn "No se encontró ~/.claude/.claude.json"
        log_warn "Deberás autenticar Claude en el EC2 manualmente: ejecuta 'claude' dentro del servidor."
    fi

    log_info "Instalando Node.js y Claude Code en el servidor..."
    ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" << 'REMOTE_SCRIPT'
        set -e
        export NVM_DIR="$HOME/.nvm"

        echo "--- Instalando nvm ---"
        if [[ ! -d "$NVM_DIR" ]]; then
            curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
        fi
        [ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"

        echo "--- Instalando Node.js LTS ---"
        nvm install --lts
        nvm use --lts
        echo "Node: $(node --version)  npm: $(npm --version)"

        echo "--- Instalando Claude Code ---"
        npm install -g @anthropic-ai/claude-code

        echo "--- Creando wrapper claude-run para systemd ---"
        sudo tee /usr/local/bin/claude-run > /dev/null << 'WRAPPER'
#!/bin/bash
export NVM_DIR="/home/ec2-user/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
exec claude "$@"
WRAPPER
        sudo chmod 755 /usr/local/bin/claude-run
        /usr/local/bin/claude-run --version && echo "claude OK" || echo "claude instalado (necesita auth)"

        echo "--- Configurando permisos ---"
        sudo chown -R ec2-user:ec2-user /opt/mcp
        sudo chmod 640 /opt/mcp/app/.env

        echo "--- Instalando servicios systemd ---"
        sudo cp /tmp/systemd_units/*.service /etc/systemd/system/
        sudo systemctl daemon-reload
        sudo systemctl enable mcp-server telegram-bot

        echo ""
        echo "════════════════════════════════════════════════════════"
        echo "  PASO MANUAL REQUERIDO: Autenticar Claude Code"
        echo "════════════════════════════════════════════════════════"
        echo "  1. Conectate al servidor:"
        echo "     ssh -i tu-key.pem ec2-user@$(curl -s http://169.254.169.254/latest/meta-data/public-ipv4)"
        echo ""
        echo "  2. Ejecuta el login de Claude:"
        echo "     claude"
        echo "     (Abre la URL en tu navegador y acepta)"
        echo ""
        echo "  3. Verifica que funciona:"
        echo "     claude -p 'Responde solo: OK'"
        echo ""
        echo "  4. Levanta los servicios:"
        echo "     sudo systemctl start mcp-server telegram-bot"
        echo "════════════════════════════════════════════════════════"
REMOTE_SCRIPT

    log_ok "Aplicación desplegada"
    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║   DESPLIEGUE COMPLETADO — FALTA AUTENTICAR CLAUDE        ║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  IP del servidor  : ${BLUE}${PUBLIC_IP}${NC}"
    echo -e "  Perfil AWS       : ${BLUE}${AWS_PROFILE}${NC}"
    echo ""
    echo -e "${YELLOW}  PRÓXIMO PASO — Autenticar Claude Code:${NC}"
    echo -e "  ${BLUE}ssh -i ${KEY_FILE} ec2-user@${PUBLIC_IP}${NC}"
    echo -e "  Dentro del EC2 ejecuta: ${BLUE}claude${NC}"
    echo -e "  Abre la URL en tu navegador y completa el login."
    echo -e "  Luego: ${BLUE}sudo systemctl start mcp-server telegram-bot${NC}"
    echo ""
    echo -e "  Ver logs: ${BLUE}sudo journalctl -u telegram-bot -f${NC}"
    echo ""
}

# ==============================================================
# Main
# ==============================================================
main() {
    echo -e "${BLUE}"
    echo "  ╔══════════════════════════════════════════╗"
    echo "  ║   Claude MCP Server (Go) — Deploy        ║"
    echo "  ╚══════════════════════════════════════════╝"
    echo -e "${NC}"

    check_prerequisites

    if [[ "$APP_ONLY" == "false" ]]; then
        deploy_infrastructure
    fi

    if [[ "$INFRA_ONLY" == "false" ]]; then
        deploy_application
    fi
}

main
