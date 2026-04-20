#!/usr/bin/env bash
# =============================================================
# deploy.sh - Despliega infraestructura y aplicación MCP en AWS
# =============================================================
set -euo pipefail

# Colores
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC}  $1"; }
log_ok()      { echo -e "${GREEN}[OK]${NC}    $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC}  $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
log_section() { echo -e "\n${BLUE}━━━ $1 ━━━${NC}"; }

# Rutas
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CFN_TEMPLATE="${ROOT_DIR}/infra/cloudformation/stack.yml"

# Configuración con valores por defecto
STACK_NAME="${STACK_NAME:-mcp-server}"
AWS_REGION="${AWS_REGION:-us-east-1}"
INSTANCE_TYPE="${INSTANCE_TYPE:-t4g.micro}"
SSH_CIDR="${SSH_CIDR:-0.0.0.0/0}"
KEY_PAIR_NAME="${KEY_PAIR_NAME:-}"
KEY_FILE="${KEY_FILE:-}"
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
  -r, --region    REGION  Región AWS (default: us-east-1)
  -t, --type      TYPE    Tipo de instancia (default: t4g.micro)
  --cidr          CIDR    CIDR permitido para SSH (default: 0.0.0.0/0)
  --infra-only            Solo desplegar infraestructura
  --app-only              Solo desplegar aplicación (infra debe existir)
  -y, --yes               No pedir confirmación
  -h, --help              Mostrar esta ayuda

Variables de entorno equivalentes:
  STACK_NAME, AWS_REGION, INSTANCE_TYPE, KEY_PAIR_NAME, KEY_FILE, SSH_CIDR

Ejemplo rápido:
  export KEY_PAIR_NAME=mi-key-pair
  export KEY_FILE=~/.ssh/mi-key.pem
  $0

Ejemplo completo:
  $0 -k mi-key -f ~/.ssh/mi-key.pem -r us-east-1 -t t4g.micro
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
        -t|--type)      INSTANCE_TYPE="$2"; shift 2 ;;
        --cidr)         SSH_CIDR="$2"; shift 2 ;;
        --infra-only)   INFRA_ONLY=true; shift ;;
        --app-only)     APP_ONLY=true; shift ;;
        -y|--yes)       SKIP_CONFIRM=true; shift ;;
        -h|--help)      show_usage; exit 0 ;;
        *) log_error "Opción desconocida: $1"; show_usage; exit 1 ;;
    esac
done

# ==============================================================
# Prerrequisitos
# ==============================================================
check_prerequisites() {
    log_section "Verificando prerrequisitos"

    local missing=false

    command -v aws  &>/dev/null || { log_error "AWS CLI no encontrado. Instala: https://aws.amazon.com/cli/"; missing=true; }
    command -v ssh  &>/dev/null || { log_error "ssh no encontrado"; missing=true; }
    command -v scp  &>/dev/null || { log_error "scp no encontrado"; missing=true; }
    command -v jq   &>/dev/null || log_warn "jq no encontrado (opcional, para debug)"

    [[ "$missing" == "true" ]] && exit 1

    # Verificar credenciales AWS
    local identity
    identity=$(aws sts get-caller-identity --region "$AWS_REGION" 2>&1) || {
        log_error "Credenciales AWS no configuradas. Ejecuta: aws configure"
        exit 1
    }
    local account
    account=$(echo "$identity" | grep -o '"Account": "[^"]*"' | cut -d'"' -f4 || echo "unknown")
    log_ok "AWS autenticado (cuenta: ${account}, región: ${AWS_REGION})"
}

# ==============================================================
# Desplegar CloudFormation
# ==============================================================
deploy_infrastructure() {
    log_section "Desplegando infraestructura CloudFormation"

    [[ -z "$KEY_PAIR_NAME" ]] && {
        log_error "KEY_PAIR_NAME requerido. Usa -k o exporta la variable."
        log_warn "Para crear un key pair: aws ec2 create-key-pair --key-name mi-key --query 'KeyMaterial' --output text > mi-key.pem"
        exit 1
    }

    [[ -f "$CFN_TEMPLATE" ]] || { log_error "Template no encontrado: ${CFN_TEMPLATE}"; exit 1; }

    log_info "Stack: ${STACK_NAME}"
    log_info "Región: ${AWS_REGION}"
    log_info "Instancia: ${INSTANCE_TYPE}"
    log_info "Key Pair: ${KEY_PAIR_NAME}"
    log_info "SSH CIDR: ${SSH_CIDR}"

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
        --region "$AWS_REGION" \
        --no-fail-on-empty-changeset

    log_ok "Infraestructura desplegada"
}

# ==============================================================
# Helpers para outputs de CloudFormation
# ==============================================================
get_output() {
    aws cloudformation describe-stacks \
        --stack-name "$STACK_NAME" \
        --region "$AWS_REGION" \
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

    log_info "Esperando que SSH esté disponible en ${ip}..."
    while (( attempt < max_attempts )); do
        if ssh -o ConnectTimeout=5 \
               -o StrictHostKeyChecking=no \
               -o BatchMode=yes \
               -i "$KEY_FILE" \
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
        log_error "KEY_FILE requerido para desplegar la app. Usa -f o exporta la variable."
        exit 1
    }
    [[ -f "$KEY_FILE" ]] || { log_error "Archivo de clave no encontrado: ${KEY_FILE}"; exit 1; }

    # Obtener IP de la instancia
    local PUBLIC_IP
    PUBLIC_IP=$(get_output "InstancePublicIP")
    [[ -z "$PUBLIC_IP" ]] && {
        log_error "No se pudo obtener la IP del stack '${STACK_NAME}'"
        log_warn "¿Está desplegada la infraestructura? Ejecuta primero sin --app-only"
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
            log_warn "IMPORTANTE: Edita ${ROOT_DIR}/.env"
            log_warn "  - TELEGRAM_BOT_TOKEN: habla con @BotFather en Telegram"
            log_warn "  (ANTHROPIC_API_KEY no es requerido si usas Claude Code CLI)"
            log_warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            read -rp "Presiona Enter después de editar .env (Ctrl+C para cancelar)..."
        else
            log_error ".env.example no encontrado en ${ROOT_DIR}"
            exit 1
        fi
    fi

    wait_for_ssh "$PUBLIC_IP"

    local SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=30 -i ${KEY_FILE}"
    local SCP_OPTS="-o StrictHostKeyChecking=no -i ${KEY_FILE}"

    log_info "Preparando directorios remotos..."
    ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" \
        "sudo mkdir -p /opt/mcp/app /opt/mcp/files /opt/mcp/logs && sudo chown -R ec2-user:ec2-user /opt/mcp/app && sudo chmod -R 755 /opt/mcp/app"

    log_info "Copiando archivos de la aplicación..."
    scp $SCP_OPTS -r "${ROOT_DIR}/src/"         "ec2-user@${PUBLIC_IP}:/opt/mcp/app/"
    scp $SCP_OPTS    "${ROOT_DIR}/requirements.txt" "ec2-user@${PUBLIC_IP}:/opt/mcp/app/"
    scp $SCP_OPTS    "${ROOT_DIR}/.env"          "ec2-user@${PUBLIC_IP}:/opt/mcp/app/.env"
    scp $SCP_OPTS -r "${ROOT_DIR}/systemd/"      "ec2-user@${PUBLIC_IP}:/tmp/systemd_units/"

    # Copiar SOLO los archivos de autenticación de Claude Code (no el historial)
    if [[ -f "${HOME}/.claude/.claude.json" ]]; then
        log_info "Copiando credenciales de autenticación de Claude Code..."
        ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" "mkdir -p ~/.claude"
        scp $SCP_OPTS "${HOME}/.claude/.claude.json" "ec2-user@${PUBLIC_IP}:~/.claude/"
        [[ -f "${HOME}/.claude/settings.json" ]] && \
            scp $SCP_OPTS "${HOME}/.claude/settings.json" "ec2-user@${PUBLIC_IP}:~/.claude/"
        log_ok "Credenciales copiadas (solo auth, sin historial)"
    else
        log_warn "No se encontró ~/.claude/.claude.json — deberás autenticar Claude en el EC2 manualmente."
        log_warn "Después del deploy: ssh al EC2 y ejecuta: claude"
    fi

    log_info "Instalando Node.js, Claude Code y dependencias Python..."
    ssh $SSH_OPTS "ec2-user@${PUBLIC_IP}" << 'REMOTE_SCRIPT'
        set -e
        export NVM_DIR="$HOME/.nvm"

        # ── Node.js via nvm ─────────────────────────────────────────
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

        # ── Wrapper script para claude (solución robusta para systemd) ──
        # Un simple symlink a ~/.nvm/... no funciona desde systemd porque
        # nvm no está cargado. Este wrapper sí funciona.
        echo "--- Creando wrapper claude-run para systemd ---"
        REAL_NODE_BIN=$(dirname $(which node))
        sudo tee /usr/local/bin/claude-run > /dev/null << WRAPPER
#!/bin/bash
export NVM_DIR="/home/ec2-user/.nvm"
[ -s "\$NVM_DIR/nvm.sh" ] && source "\$NVM_DIR/nvm.sh"
exec claude "\$@"
WRAPPER
        sudo chmod 755 /usr/local/bin/claude-run
        echo "Wrapper creado: /usr/local/bin/claude-run"

        # Verificar que claude se puede invocar
        /usr/local/bin/claude-run --version && echo "claude OK" || echo "claude instalado (necesita auth)"

        # ── Python dependencies ──────────────────────────────────────
        echo "--- Instalando dependencias Python ---"
        cd /opt/mcp/app
        python3.11 -m pip install --upgrade pip -q
        python3.11 -m pip install -r requirements.txt -q

        # ── Permisos ─────────────────────────────────────────────────
        # chown ANTES de chmod para que ec2-user sea el dueño
        echo "--- Configurando permisos ---"
        sudo chown -R ec2-user:ec2-user /opt/mcp/app
        sudo chown -R ec2-user:ec2-user /opt/mcp/logs
        sudo chown -R ec2-user:ec2-user /opt/mcp/files
        # 644 = ec2-user puede leer/escribir, otros solo leer
        sudo chmod 644 /opt/mcp/app/.env

        # ── Servicios systemd ────────────────────────────────────────
        echo "--- Instalando servicios systemd ---"
        sudo cp /tmp/systemd_units/*.service /etc/systemd/system/
        sudo systemctl daemon-reload
        sudo systemctl enable mcp-server telegram-bot

        echo ""
        echo "════════════════════════════════════════════════════════"
        echo "  PASO MANUAL REQUERIDO: Autenticar Claude Code"
        echo "════════════════════════════════════════════════════════"
        echo "  1. Conectate a esta instancia:"
        echo "     ssh -i tu-key.pem ec2-user@$(curl -s http://169.254.169.254/latest/meta-data/public-ipv4)"
        echo ""
        echo "  2. Ejecuta el login de Claude:"
        echo "     claude"
        echo "     (Se abre una URL - ábrela en tu navegador y acepta)"
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
    echo -e "${GREEN}║   DESPLIEGUE COMPLETADO - FALTA AUTENTICAR CLAUDE        ║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  IP de la instancia : ${BLUE}${PUBLIC_IP}${NC}"
    echo ""
    echo -e "${YELLOW}  PRÓXIMO PASO - Autenticar Claude Code en EC2:${NC}"
    echo -e "  ${BLUE}ssh -i ${KEY_FILE} ec2-user@${PUBLIC_IP}${NC}"
    echo -e "  Dentro del EC2 ejecuta: ${BLUE}claude${NC}"
    echo -e "  Abre la URL en tu navegador y completa el login."
    echo -e "  Luego ejecuta: ${BLUE}sudo systemctl start mcp-server telegram-bot${NC}"
    echo ""
    echo -e "  Ver logs : ${BLUE}sudo journalctl -u telegram-bot -f${NC}"
    echo ""
}

# ==============================================================
# Main
# ==============================================================
main() {
    echo -e "${BLUE}"
    echo "  ╔══════════════════════════════════════════╗"
    echo "  ║   Claude MCP Server - Deploy Script      ║"
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
