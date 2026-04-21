# Configuración

## Variables de entorno (.env)

Copia `.env.example` a `.env` y completa cada valor:

```bash
cp .env.example .env
nano .env
```

### Telegram

| Variable | Requerida | Descripción |
|----------|-----------|-------------|
| `TELEGRAM_BOT_TOKEN` | ✅ | Token del bot (obtenlo de @BotFather) |
| `ALLOWED_TELEGRAM_USERS` | Recomendada | IDs o @usernames autorizados, separados por coma |
| `NOTIFICATION_CHAT_IDS` | Opcional | Chat IDs que recibirán notificaciones de PR |

**Obtener tu Telegram User ID:**
1. Habla con `@userinfobot` en Telegram
2. Te responde con tu ID numérico

**Crear un bot:**
1. Habla con `@BotFather` → `/newbot`
2. Sigue las instrucciones
3. Copia el token

### GitHub — Cuenta Miatech

| Variable | Descripción |
|----------|-------------|
| `GITHUB_MIATECH_TOKEN` | PAT con permisos `repo`, `read:org` |
| `GITHUB_MIATECH_ORG` | Nombre de la organización Miatech en GitHub |

**Crear PAT (Personal Access Token):**
1. GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
2. Generate new token
3. Scopes: `repo`, `read:org`
4. Copia el token generado

### GitHub — Cuenta Aeromexico

| Variable | Descripción |
|----------|-------------|
| `GITHUB_AEROMEXICO_TOKEN` | PAT con permisos `repo`, `workflow`, `write:org` |
| `GITHUB_AEROMEXICO_ORG` | Nombre de la organización Aeromexico en GitHub |

**Scopes requeridos** (necesita más permisos porque crea PRs y ramas):
- `repo` — acceso completo a repositorios
- `workflow` — disparar GitHub Actions
- `read:org` + `write:org` si crea repos o gestiona permisos

### Repositorios

| Variable | Descripción |
|----------|-------------|
| `REPO_FACTURACION` | Nombre del repo de Facturación (sin la org) |
| `REPO_RAM` | Nombre del repo de RAM (sin la org) |

### MCP Server

| Variable | Default | Descripción |
|----------|---------|-------------|
| `MCP_SERVER_PORT` | `8080` | Puerto SSE (clientes Claude) |
| `MCP_API_PORT` | `8081` | Puerto API interna (bot) |
| `MCP_BASE_URL` | `http://127.0.0.1:8080` | URL externa del MCP server |
| `MCP_SERVER_HOST` | `127.0.0.1` | Host del MCP server (Bot usa la IP privada EC2) |

### Bot → MCP

En el servidor del **Bot**, `MCP_SERVER_HOST` debe ser la **IP privada** del MCP server EC2.
El script de deploy la configura automáticamente.

En desarrollo local, usa `127.0.0.1` si corres ambos servicios en la misma máquina.

### Deploy script

| Variable | Default | Descripción |
|----------|---------|-------------|
| `DEPLOY_SCRIPT_PATH` | `/opt/mcp-deploy/scripts/github-deploy.sh` | Ruta al script bash |
| `LOGS_DIR` | `/opt/mcp-deploy/logs` | Directorio de logs |

## Instalación de clientes requeridos

### AWS CLI v2

```bash
# Linux ARM64 (EC2 Graviton)
curl "https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# Linux x86_64 (máquina local)
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# macOS
brew install awscli

# Configurar perfil
aws configure --profile mcp-deploy
# AWS Access Key ID: <tu-access-key>
# AWS Secret Access Key: <tu-secret-key>
# Default region: us-east-2
# Default output format: json
```

### Go 1.23+

```bash
# Linux (descarga directa)
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version

# macOS
brew install go
```

### GitHub CLI (gh)

```bash
# Amazon Linux 2023 (en EC2)
sudo dnf install -y 'dnf-command(config-manager)'
sudo dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
sudo dnf install -y gh

# Ubuntu/Debian
sudo apt install gh

# macOS
brew install gh

# Autenticar con token Miatech
export GH_TOKEN=ghp_xxxxxx
gh auth status

# Autenticar con token Aeromexico
export GH_TOKEN=ghp_yyyyyy
gh auth status
```

### ssh y scp

Incluidos en la mayoría de sistemas. En Windows usa Git Bash o WSL.

## Configurar AWS Key Pair

```bash
# Crear key pair (solo una vez)
aws --profile mcp-deploy ec2 create-key-pair \
    --key-name mcp-deploy-key \
    --query 'KeyMaterial' \
    --output text \
    --region us-east-2 > ~/.ssh/mcp-deploy-key.pem

chmod 400 ~/.ssh/mcp-deploy-key.pem

# Exportar para el script de deploy
export KEY_PAIR_NAME=mcp-deploy-key
export KEY_FILE=~/.ssh/mcp-deploy-key.pem
```
