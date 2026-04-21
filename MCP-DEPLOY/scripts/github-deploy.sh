#!/usr/bin/env bash
# =============================================================================
# github-deploy.sh
# Script de deploy via GitHub: copia el repo de Miatech a Aeromexico y crea PR.
# GitHub Actions en la organización de Aeromexico se encarga del deploy real.
#
# LLAMADO POR: MCP server (deploy.go → Execute)
# USO: ./github-deploy.sh <environment> <service>
#
# ARGUMENTOS:
#   environment  — dev | qa | prod
#   service      — facturacion | ram
#
# VARIABLES DE ENTORNO (inyectadas por el MCP server):
#   GITHUB_MIATECH_TOKEN      Token PAT de la cuenta Miatech
#   GITHUB_MIATECH_ORG        Organización Miatech en GitHub
#   GITHUB_AEROMEXICO_TOKEN   Token PAT de la cuenta Aeromexico
#   GITHUB_AEROMEXICO_ORG     Organización Aeromexico en GitHub
#   REPO_FACTURACION          Nombre del repositorio Facturación
#   REPO_RAM                  Nombre del repositorio RAM
#   REQUESTED_BY              Username de quien solicitó el deploy
#
# SALIDA REQUERIDA:
#   El script DEBE imprimir la URL del PR en este formato:
#   PR_URL: https://github.com/<org>/<repo>/pull/<number>
# =============================================================================
set -euo pipefail

# ─── Colores ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()  { echo -e "${BLUE}[INFO]${NC}  $1" >&2; }
log_ok()    { echo -e "${GREEN}[OK]${NC}    $1" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $1" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }

# ─── Argumentos ───────────────────────────────────────────────────────────────
ENVIRONMENT="${1:?Error: se requiere el argumento environment (dev|qa|prod)}"
SERVICE="${2:?Error: se requiere el argumento service (facturacion|ram)}"

# ─── Variables de entorno ─────────────────────────────────────────────────────
GITHUB_MIATECH_TOKEN="${GITHUB_MIATECH_TOKEN:?Variable GITHUB_MIATECH_TOKEN no definida}"
GITHUB_MIATECH_ORG="${GITHUB_MIATECH_ORG:?Variable GITHUB_MIATECH_ORG no definida}"
GITHUB_AEROMEXICO_TOKEN="${GITHUB_AEROMEXICO_TOKEN:?Variable GITHUB_AEROMEXICO_TOKEN no definida}"
GITHUB_AEROMEXICO_ORG="${GITHUB_AEROMEXICO_ORG:?Variable GITHUB_AEROMEXICO_ORG no definida}"
REPO_FACTURACION="${REPO_FACTURACION:?Variable REPO_FACTURACION no definida}"
REPO_RAM="${REPO_RAM:?Variable REPO_RAM no definida}"
REQUESTED_BY="${REQUESTED_BY:-unknown}"

# ─── Seleccionar repositorio ──────────────────────────────────────────────────
case "$SERVICE" in
  facturacion) REPO_NAME="$REPO_FACTURACION" ;;
  ram)         REPO_NAME="$REPO_RAM" ;;
  *)
    log_error "Servicio desconocido: $SERVICE (opciones: facturacion, ram)"
    exit 1
    ;;
esac

# ─── Validar ambiente ─────────────────────────────────────────────────────────
case "$ENVIRONMENT" in
  dev|qa|prod) ;;
  *)
    log_error "Ambiente desconocido: $ENVIRONMENT (opciones: dev, qa, prod)"
    exit 1
    ;;
esac

# ─── Preparar directorio temporal ────────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

log_info "Deploy: $SERVICE → $ENVIRONMENT  |  Solicitado por: @$REQUESTED_BY"
log_info "Directorio de trabajo: $WORK_DIR"

# ─── Configurar gh CLI con tokens ─────────────────────────────────────────────
# Autenticar gh con la cuenta Miatech para clonar el repo fuente.
export GH_TOKEN="$GITHUB_MIATECH_TOKEN"

# ─── Clonar repositorio fuente (Miatech) ─────────────────────────────────────
SOURCE_REPO="${GITHUB_MIATECH_ORG}/${REPO_NAME}"
log_info "Clonando repositorio fuente: $SOURCE_REPO"

gh repo clone "$SOURCE_REPO" "$WORK_DIR/source" -- --depth=1 --branch=main 2>&1 || {
  log_error "No se pudo clonar $SOURCE_REPO"
  exit 1
}
log_ok "Repositorio fuente clonado"

# ─── Cambiar al token de Aeromexico ──────────────────────────────────────────
export GH_TOKEN="$GITHUB_AEROMEXICO_TOKEN"

# ─── Verificar que el repo destino existe en Aeromexico ──────────────────────
DEST_REPO="${GITHUB_AEROMEXICO_ORG}/${REPO_NAME}"
log_info "Verificando repositorio destino: $DEST_REPO"

gh repo view "$DEST_REPO" &>/dev/null || {
  log_error "El repositorio destino $DEST_REPO no existe o no tienes acceso"
  exit 1
}

# ─── Clonar repositorio destino (Aeromexico) ──────────────────────────────────
log_info "Clonando repositorio destino: $DEST_REPO"
gh repo clone "$DEST_REPO" "$WORK_DIR/dest" -- --depth=1 2>&1 || {
  log_error "No se pudo clonar $DEST_REPO"
  exit 1
}
log_ok "Repositorio destino clonado"

# ─── Crear rama de deploy ─────────────────────────────────────────────────────
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
BRANCH_NAME="deploy/${ENVIRONMENT}/${SERVICE}/${TIMESTAMP}"

cd "$WORK_DIR/dest"
git checkout -b "$BRANCH_NAME"
log_ok "Rama de deploy creada: $BRANCH_NAME"

# ─── Copiar archivos del repo fuente al destino ──────────────────────────────
# AJUSTA ESTA SECCIÓN según la estructura real de tus repositorios.
# Ejemplo: copiar carpeta src/ y config/ del ambiente correspondiente.
log_info "Copiando archivos del repo fuente..."

rsync -av --exclude='.git' \
  "$WORK_DIR/source/" \
  "$WORK_DIR/dest/" \
  2>&1 || {
  log_error "Error al copiar archivos"
  exit 1
}

# ─── Commit de los cambios ────────────────────────────────────────────────────
git config user.email "deploy-bot@aeromexico.com"
git config user.name "MCP Deploy Bot"

git add -A

if git diff --cached --quiet; then
  log_warn "No hay cambios para commitear — el deploy podría ser idempotente"
  # Crear un commit vacío para forzar el PR si se requiere
  git commit --allow-empty -m "deploy($ENVIRONMENT/$SERVICE): triggered by @$REQUESTED_BY at $TIMESTAMP"
else
  git commit -m "deploy($ENVIRONMENT/$SERVICE): sync from miatech/$REPO_NAME at $TIMESTAMP

Solicitado por: @$REQUESTED_BY
Ambiente: $ENVIRONMENT
Servicio: $SERVICE
Timestamp: $TIMESTAMP"
fi

# ─── Push de la rama ─────────────────────────────────────────────────────────
log_info "Haciendo push de la rama $BRANCH_NAME..."
git push \
  "https://x-access-token:${GITHUB_AEROMEXICO_TOKEN}@github.com/${DEST_REPO}.git" \
  "$BRANCH_NAME" 2>&1

log_ok "Push completado"

# ─── Crear Pull Request en Aeromexico ────────────────────────────────────────
TARGET_BRANCH="${ENVIRONMENT}"  # Asume que existe una rama por ambiente: dev, qa, prod
# Si el nombre de rama destino es diferente, ajusta aquí.
# Por ejemplo: TARGET_BRANCH="main" para prod.

PR_TITLE="[${ENVIRONMENT^^}] Deploy ${SERVICE} - @${REQUESTED_BY} - ${TIMESTAMP}"
PR_BODY="## Deploy Automático

| Campo | Valor |
|-------|-------|
| Servicio | ${SERVICE} |
| Ambiente | ${ENVIRONMENT} |
| Solicitado por | @${REQUESTED_BY} |
| Fuente | ${SOURCE_REPO} |
| Timestamp | ${TIMESTAMP} |

> Deploy disparado via MCP Deploy Bot"

log_info "Creando Pull Request..."
PR_URL=$(gh pr create \
  --repo "$DEST_REPO" \
  --title "$PR_TITLE" \
  --body "$PR_BODY" \
  --base "$TARGET_BRANCH" \
  --head "$BRANCH_NAME" \
  2>&1) || {
  log_error "No se pudo crear el PR: $PR_URL"
  exit 1
}

log_ok "PR creado exitosamente"

# ─── Output requerido (el MCP server extrae esta línea) ───────────────────────
echo "PR_URL: $PR_URL"
