#!/usr/bin/env bash
# =============================================================================
# deploy.sh — Despliega infraestructura y aplicación MCP Deploy en AWS
#
# PRERREQUISITOS (instalar antes de ejecutar):
#   - AWS CLI v2:   https://docs.aws.amazon.com/cli/latest/userguide/install-cliv2.html
#   - Go 1.23+:     https://go.dev/dl/
#   - gh (GitHub CLI): https://cli.github.com/
#   - ssh + scp (incluidos en la mayoría de sistemas)
#
# USO:
#   export KEY_PAIR_NAME=mi-key-pair
#   export KEY_FILE=~/.ssh/mi-key.pem
#   ./deploy.sh
#
# O con argumentos:
#   ./deploy.sh -k mi-key -f ~/.ssh/mi-key.pem -r us-east-2 -p mcp-deploy
# =============================================================================
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

STACK_NAME="${STACK_NAME:-mcp-deploy}"
AWS_REGION="${AWS_REGION:-us-east-2}"
AWS_PROFILE="${AWS_PROFILE:-mcp-demo}"
INSTANCE_TYPE="${INSTANCE_TYPE:-t4g.micro}"
SSH_CIDR="${SSH_CIDR:-0.0.0.0/0}"
KEY_PAIR_NAME="${KEY_PAIR_NAME:-key_pair_mcp_demo}"
KEY_FILE="${KEY_FILE:-$HOME/.ssh/key_pair_mcp_demo.pem}"
INFRA_ONLY=false
APP_ONLY=false
SKIP_CONFIRM=false

show_usage() {
    cat <<EOF
Uso: $0 [OPCIONES]

Opciones:
  -k, --key-pair NAME     Nombre del Key Pair EC2 en AWS (requerido para infra)
  -f, --key-file  PATH    Ruta al archivo .pem para SSH (requerido para app)
  -s, --stack     NAME    Stack CloudFormation (default: mcp-deploy)
  -r, --region    REGION  Región AWS (default: us-east-2)
  -p, --profile   PROFILE Perfil AWS CLI (default: mcp-deploy)
  -t, --type      TYPE    Tipo de instancia (default: t4g.micro)
  --cidr          CIDR    CIDR para SSH (default: 0.0.0.0/0)
  --infra-only            Solo infraestructura
  --app-only              Solo aplicación (infra ya debe existir)
  -y, --yes               Sin confirmación interactiva
  -h, --help              Esta ayuda

Variables de entorno equivalentes:
  STACK_NAME, AWS_REGION, AWS_PROFILE, INSTANCE_TYPE, KEY_PAIR_NAME, KEY_FILE, SSH_CIDR

Ejemplo rápido:
  export KEY_PAIR_NAME=mi-key
  export KEY_FILE=~/.ssh/mi-key.pem
  $0
EOF
}

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

aws() { command aws --profile "$AWS_PROFILE" --region "$AWS_REGION" "$@"; }

# ─── Prerrequisitos ───────────────────────────────────────────────────────────
check_prerequisites() {
    log_section "Verificando prerrequisitos"
    local missing=false

    for tool in aws go ssh scp gh; do
        if ! command -v "$tool" &>/dev/null; then
            log_error "$tool no encontrado"
            missing=true
        fi
    done

    [[ "$missing" == "true" ]] && {
        echo ""
        log_warn "Instala los prerrequisitos faltantes:"
        log_warn "  AWS CLI:  https://docs.aws.amazon.com/cli/latest/userguide/install-cliv2.html"
        log_warn "  Go:       https://go.dev/dl/"
        log_warn "  gh CLI:   https://cli.github.com/"
        exit 1
    }

    local identity
    identity=$(aws sts get-caller-identity 2>&1) || {
        log_error "Credenciales AWS inválidas para el perfil '${AWS_PROFILE}'"
        log_warn "Configura con: aws configure --profile ${AWS_PROFILE}"
        exit 1
    }
    local account
    account=$(echo "$identity" | grep -o '"Account": "[^"]*"' | cut -d'"' -f4)
    log_ok "AWS OK — perfil: ${AWS_PROFILE} | cuenta: ${account} | región: ${AWS_REGION}"
    log_ok "Go: $(go version | awk '{print $3}')"
    log_ok "gh: $(gh --version | head -1)"
}

# ─── Compilar binarios Go para linux/arm64 ────────────────────────────────────
build_binaries() {
    log_section "Compilando binarios Go (linux/arm64)"
    mkdir -p "${ROOT_DIR}/bin"

    # Descargar dependencias si hace falta
    (cd "${ROOT_DIR}" && go mod download)

    log_info "Compilando telegram-bot..."
    GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" \
        -o "${ROOT_DIR}/bin/telegram-bot" "${ROOT_DIR}/cmd/bot/"

    log_info "Compilando mcp-server..."
    GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" \
        -o "${ROOT_DIR}/bin/mcp-server" "${ROOT_DIR}/cmd/mcp-server/"

    log_ok "Binarios compilados:"
    ls -lh "${ROOT_DIR}/bin/"
}

# ─── Desplegar CloudFormation ─────────────────────────────────────────────────
deploy_infrastructure() {
    log_section "Desplegando infraestructura CloudFormation"

    [[ -z "$KEY_PAIR_NAME" ]] && {
        log_error "KEY_PAIR_NAME requerido. Usa -k o exporta la variable."
        log_warn "Para crear un key pair:"
        log_warn "  aws --profile ${AWS_PROFILE} ec2 create-key-pair \\"
        log_warn "      --key-name mi-key --query 'KeyMaterial' --output text > mi-key.pem"
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
        read -rp "¿Continuar con el despliegue de infraestructura? [y/N] " confirm
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

# ─── Helpers CloudFormation ───────────────────────────────────────────────────
get_output() {
    aws cloudformation describe-stacks \
        --stack-name "$STACK_NAME" \
        --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" \
        --output text 2>/dev/null || echo ""
}

# ─── Esperar SSH ──────────────────────────────────────────────────────────────
wait_for_ssh() {
    local ip="$1" label="$2"
    local max=36 attempt=0

    log_info "Esperando SSH en ${label} (${ip})..."
    while (( attempt < max )); do
        if ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=no \
               -o BatchMode=yes -i "$KEY_FILE" "ec2-user@${ip}" true 2>/dev/null; then
            echo ""
            log_ok "SSH disponible en ${label}"
            return 0
        fi
        (( attempt++ ))
        echo -n "."
        sleep 10
    done
    echo ""
    log_error "SSH no disponible en ${label} tras $((max * 10))s"
    exit 1
}

# ─── Desplegar MCP Server ─────────────────────────────────────────────────────
# Nota: MCP_PRIVATE_IP es global — deploy_application() lo lee después de llamar aquí
MCP_PRIVATE_IP=""

deploy_mcp_server() {
    local MCP_PUBLIC_IP
    MCP_PUBLIC_IP=$(aws ec2 describe-instances \
        --filters "Name=tag:Name,Values=${STACK_NAME}-mcp-server" "Name=instance-state-name,Values=running" \
        --query "Reservations[0].Instances[0].PublicIpAddress" --output text 2>/dev/null || echo "")
    MCP_PRIVATE_IP=$(get_output "MCPPrivateIP")

    [[ -z "$MCP_PUBLIC_IP" || "$MCP_PUBLIC_IP" == "None" ]] && {
        log_error "No se pudo obtener la IP pública del MCP server"
        exit 1
    }
    log_info "MCP Server — IP pública: ${MCP_PUBLIC_IP} | IP privada: ${MCP_PRIVATE_IP}"

    local SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=30 -i ${KEY_FILE}"
    local SCP_OPTS="-o StrictHostKeyChecking=no -i ${KEY_FILE}"

    wait_for_ssh "$MCP_PUBLIC_IP" "MCP Server"

    log_info "Preparando directorios en MCP server..."
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" \
        "sudo mkdir -p /opt/mcp-deploy/{app,bin,logs} && \
         sudo chown -R ec2-user:ec2-user /opt/mcp-deploy && \
         mkdir -p /home/ec2-user/repos/FEBOL"

    log_info "Copiando binario mcp-server..."
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" "sudo systemctl stop mcp-server 2>/dev/null || true"
    scp $SCP_OPTS "${ROOT_DIR}/bin/mcp-server" "ec2-user@${MCP_PUBLIC_IP}:/opt/mcp-deploy/bin/"
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" "chmod +x /opt/mcp-deploy/bin/mcp-server"

    # am-febol-devops vive junto al directorio MCP-DEPLOY en el repo local
    FEBOL_DEVOPS_DIR="$(cd "${ROOT_DIR}/../am-febol-devops" 2>/dev/null && pwd)" || {
        log_error "No se encontró am-febol-devops/ en ${ROOT_DIR}/../"
        log_warn "Asegúrate de tener la carpeta am-febol-devops/ al lado de MCP-DEPLOY/"
        exit 1
    }
    log_info "Copiando am-febol-devops a MCP server (deployer.sh + scripts)..."
    rsync -az --exclude='.git' \
        -e "ssh -o StrictHostKeyChecking=no -i ${KEY_FILE}" \
        "${FEBOL_DEVOPS_DIR}/" \
        "ec2-user@${MCP_PUBLIC_IP}:/home/ec2-user/repos/FEBOL/am-febol-devops/"
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" \
        "chmod +x /home/ec2-user/repos/FEBOL/am-febol-devops/scripts/*.sh"
    log_ok "Scripts copiados — deployer.sh en /home/ec2-user/repos/FEBOL/am-febol-devops/scripts/"

    log_info "Copiando configuración .env al MCP server..."
    scp $SCP_OPTS "${ROOT_DIR}/.env" "ec2-user@${MCP_PUBLIC_IP}:/opt/mcp-deploy/app/.env"
    # Actualizar DEPLOY_SCRIPT_PATH para que apunte al deployer.sh recién copiado
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" \
        "sed -i 's|^DEPLOY_SCRIPT_PATH=.*|DEPLOY_SCRIPT_PATH=/home/ec2-user/repos/FEBOL/am-febol-devops/scripts/deployer.sh|' \
         /opt/mcp-deploy/app/.env && chmod 640 /opt/mcp-deploy/app/.env"

    # ── Leer tokens GitHub del .env local ──────────────────────────────────────
    local MIATECH_TOKEN AMX_TOKEN
    MIATECH_TOKEN=$(grep '^GITHUB_MIATECH_TOKEN=' "${ROOT_DIR}/.env" | cut -d'=' -f2-)
    AMX_TOKEN=$(grep '^GITHUB_AEROMEXICO_TOKEN=' "${ROOT_DIR}/.env" | cut -d'=' -f2-)

    [[ -z "$MIATECH_TOKEN" ]] && {
        log_error "GITHUB_MIATECH_TOKEN no configurado en .env"
        log_warn "Agrega el token PAT de Miatech (permisos: repo, read:org)"
        exit 1
    }
    [[ -z "$AMX_TOKEN" ]] && {
        log_error "GITHUB_AEROMEXICO_TOKEN no configurado en .env"
        log_warn "Agrega el token PAT de Aeromexico (permisos: repo, workflow, read:org, write:org)"
        exit 1
    }

    log_info "Autenticando cuentas GitHub en MCP server..."
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" \
        "printf '%s' '${MIATECH_TOKEN}' | gh auth login --hostname github.com --with-token"
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" \
        "printf '%s' '${AMX_TOKEN}' | gh auth login --hostname github.com --with-token"
    log_ok "Cuentas GitHub autenticadas: $(ssh $SSH_OPTS ec2-user@${MCP_PUBLIC_IP} 'gh auth status 2>&1 | grep Logged' | tr '\n' ' ')"

    log_info "Clonando repositorios Miatech en MCP server..."
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" "
        mkdir -p ~/repos/FEBOL/miatech
        cd ~/repos/FEBOL/miatech
        for repo in am-fe-mx-api mi-fe-mx-front mi-fe-mx-front-individual; do
            if [ -d \"\$repo\" ]; then
                echo \"  \$repo: actualizando...\"
                git -C \"\$repo\" pull --ff-only 2>/dev/null || true
            else
                echo \"  \$repo: clonando...\"
                git clone "https://x-access-token:${MIATECH_TOKEN}@github.com/miatechinternational/\$repo.git"
            fi
        done
    "

    log_info "Clonando repositorios Aeromexico en MCP server..."
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" "
        mkdir -p ~/repos/FEBOL/am
        cd ~/repos/FEBOL/am
        for repo in FEBOL_MX_Backend_Read FEBOL_MX_Backend_CronJobs FEBOL_MX_Backend_Timbrado \
                    FEBOL_MX_Backend_Input FEBOL_MX_Backend_Write FEBOL_MX_Backend_IRead \
                    FEBOL_MX_Backend_IWrite FEBOL_MX_Portal_Individual FEBOL_MX_Portal; do
            if [ -d \"\$repo\" ]; then
                echo \"  \$repo: actualizando...\"
                git -C \"\$repo\" pull --ff-only 2>/dev/null || true
            else
                echo \"  \$repo: clonando...\"
                git clone "https://x-access-token:${AMX_TOKEN}@github.com/BO-AMX/\$repo.git"
            fi
        done
    "
    log_ok "Repositorios clonados en ~/repos/FEBOL/"

    log_info "Instalando servicio systemd del MCP server..."
    scp $SCP_OPTS "${ROOT_DIR}/systemd/mcp-server.service" \
        "ec2-user@${MCP_PUBLIC_IP}:/tmp/"
    ssh $SSH_OPTS "ec2-user@${MCP_PUBLIC_IP}" "
        sudo cp /tmp/mcp-server.service /etc/systemd/system/
        sudo systemctl daemon-reload
        sudo systemctl enable mcp-server
        sudo systemctl restart mcp-server
        sleep 2
        sudo systemctl status mcp-server --no-pager || true
    "
    log_ok "MCP Server desplegado"
}

# ─── Desplegar Bot ────────────────────────────────────────────────────────────
deploy_bot() {
    local MCP_PRIVATE_IP="$1"
    local BOT_PUBLIC_IP
    BOT_PUBLIC_IP=$(get_output "BotPublicIP")

    [[ -z "$BOT_PUBLIC_IP" ]] && {
        log_error "No se pudo obtener la IP del Bot"
        exit 1
    }
    log_info "Bot Telegram — IP pública: ${BOT_PUBLIC_IP}"

    local SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=30 -i ${KEY_FILE}"
    local SCP_OPTS="-o StrictHostKeyChecking=no -i ${KEY_FILE}"

    wait_for_ssh "$BOT_PUBLIC_IP" "Bot Telegram"

    log_info "Preparando directorios en Bot..."
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" \
        "sudo mkdir -p /opt/mcp-deploy/{app,bin,logs} && \
         sudo chown -R ec2-user:ec2-user /opt/mcp-deploy"

    log_info "Copiando binario telegram-bot..."
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" "sudo systemctl stop telegram-bot 2>/dev/null || true"
    scp $SCP_OPTS "${ROOT_DIR}/bin/telegram-bot" "ec2-user@${BOT_PUBLIC_IP}:/opt/mcp-deploy/bin/"
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" "chmod +x /opt/mcp-deploy/bin/telegram-bot"

    # Crear .env para el bot con la IP privada del MCP server
    log_info "Configurando .env del Bot con MCP_SERVER_HOST=${MCP_PRIVATE_IP}..."
    # Copiar .env base y actualizar MCP_SERVER_HOST
    scp $SCP_OPTS "${ROOT_DIR}/.env" "ec2-user@${BOT_PUBLIC_IP}:/opt/mcp-deploy/app/.env"
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" "
        sed -i 's|^MCP_SERVER_HOST=.*|MCP_SERVER_HOST=${MCP_PRIVATE_IP}|' /opt/mcp-deploy/app/.env
        chmod 640 /opt/mcp-deploy/app/.env
    "

    log_info "Instalando Node.js + Claude Code en el Bot..."
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" << 'REMOTE'
        set -e
        export NVM_DIR="$HOME/.nvm"
        if [[ ! -d "$NVM_DIR" ]]; then
            curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
        fi
        [ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
        nvm install --lts
        nvm use --lts
        npm install -g @anthropic-ai/claude-code

        # Wrapper para que systemd encuentre claude sin NVM
        sudo tee /usr/local/bin/claude-run > /dev/null << 'WRAPPER'
#!/bin/bash
export NVM_DIR="/home/ec2-user/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
exec claude "$@"
WRAPPER
        sudo chmod 755 /usr/local/bin/claude-run
        echo "Claude disponible: $(/usr/local/bin/claude-run --version 2>&1 | head -1)"
REMOTE

    # Apuntar CLAUDE_BIN al wrapper que funciona sin nvm en systemd
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" \
        "sed -i 's|^CLAUDE_BIN=.*|CLAUDE_BIN=/usr/local/bin/claude-run|' /opt/mcp-deploy/app/.env"

    # Copiar credenciales Claude Code si existen localmente
    if [[ -f "${HOME}/.claude/.claude.json" ]]; then
        log_info "Copiando credenciales Claude Code..."
        ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" "mkdir -p ~/.claude"
        scp $SCP_OPTS "${HOME}/.claude/.claude.json" "ec2-user@${BOT_PUBLIC_IP}:~/.claude/"
        [[ -f "${HOME}/.claude/settings.json" ]] && \
            scp $SCP_OPTS "${HOME}/.claude/settings.json" "ec2-user@${BOT_PUBLIC_IP}:~/.claude/"
        log_ok "Credenciales Claude copiadas"
    else
        log_warn "No se encontró ~/.claude/.claude.json"
        log_warn "Deberás autenticar Claude manualmente en el Bot EC2:"
        log_warn "  ssh -i ${KEY_FILE} ec2-user@${BOT_PUBLIC_IP}"
        log_warn "  claude   (sigue las instrucciones de login)"
    fi

    log_info "Instalando servicio systemd del Bot..."
    scp $SCP_OPTS "${ROOT_DIR}/systemd/telegram-bot.service" \
        "ec2-user@${BOT_PUBLIC_IP}:/tmp/"
    ssh $SSH_OPTS "ec2-user@${BOT_PUBLIC_IP}" "
        sudo cp /tmp/telegram-bot.service /etc/systemd/system/
        sudo systemctl daemon-reload
        sudo systemctl enable telegram-bot
        sudo systemctl restart telegram-bot
        sleep 2
        sudo systemctl status telegram-bot --no-pager || true
    "
    log_ok "Bot Telegram desplegado"
}

# ─── Desplegar aplicación completa ────────────────────────────────────────────
deploy_application() {
    log_section "Desplegando aplicación"

    [[ -z "$KEY_FILE" ]] && { log_error "KEY_FILE requerido. Usa -f."; exit 1; }
    [[ -f "$KEY_FILE" ]] || { log_error "Archivo .pem no encontrado: ${KEY_FILE}"; exit 1; }
    chmod 400 "$KEY_FILE"

    # Verificar/crear .env
    if [[ ! -f "${ROOT_DIR}/.env" ]]; then
        if [[ -f "${ROOT_DIR}/.env.example" ]]; then
            log_warn ".env no encontrado. Copiando desde .env.example..."
            cp "${ROOT_DIR}/.env.example" "${ROOT_DIR}/.env"
            log_warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            log_warn "IMPORTANTE: Edita ${ROOT_DIR}/.env con estos valores:"
            log_warn "  - TELEGRAM_BOT_TOKEN       (token de @BotFather)"
            log_warn "  - ALLOWED_TELEGRAM_USERS   (IDs de Telegram autorizados)"
            log_warn "  - NOTIFICATION_CHAT_IDS    (chat donde llegan alertas)"
            log_warn "  - GITHUB_MIATECH_TOKEN     (PAT de la cuenta Miatech)"
            log_warn "  - GITHUB_AEROMEXICO_TOKEN  (PAT de la cuenta Aeromexico)"
            log_warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            read -rp "Presiona Enter después de editar .env (Ctrl+C para cancelar)..."
        else
            log_error ".env.example no encontrado"
            exit 1
        fi
    fi

    build_binaries

    deploy_mcp_server
    deploy_bot "$MCP_PRIVATE_IP"

    local BOT_PUBLIC_IP
    BOT_PUBLIC_IP=$(get_output "BotPublicIP")

    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║         DESPLIEGUE COMPLETADO EXITOSAMENTE               ║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  Bot Telegram IP  : ${BLUE}${BOT_PUBLIC_IP}${NC}"
    echo -e "  MCP Server IP    : ${BLUE}${MCP_PRIVATE_IP}${NC} (privada)"
    echo -e "  SSH Bot          : ${BLUE}ssh -i ${KEY_FILE} ec2-user@${BOT_PUBLIC_IP}${NC}"
    echo ""
    echo -e "  Logs Bot         : ${BLUE}sudo journalctl -u telegram-bot -f${NC}"
    echo -e "  Logs MCP         : ${BLUE}sudo journalctl -u mcp-server -f${NC}"
    echo ""
    echo -e "  Health check MCP : ${BLUE}curl http://${MCP_PRIVATE_IP}:8081/internal/health${NC}"
    echo ""
}

# ─── Main ─────────────────────────────────────────────────────────────────────
main() {
    echo -e "${BLUE}"
    echo "  ╔══════════════════════════════════════════╗"
    echo "  ║   MCP Deploy (Go) — Deploy Script        ║"
    echo "  ╚══════════════════════════════════════════╝"
    echo -e "${NC}"

    check_prerequisites

    [[ "$APP_ONLY" == "false" ]] && deploy_infrastructure
    [[ "$INFRA_ONLY" == "false" ]] && deploy_application
}

main
