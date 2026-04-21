# Despliegue en AWS

## Prerrequisitos

Antes de ejecutar cualquier script, asegúrate de tener instalado:

| Herramienta | Instalación |
|-------------|-------------|
| AWS CLI v2 | `brew install awscli` / [instalador oficial](https://aws.amazon.com/cli/) |
| Go 1.23+ | `brew install go` / [go.dev/dl](https://go.dev/dl/) |
| gh CLI | `brew install gh` / [cli.github.com](https://cli.github.com/) |
| ssh + scp | Incluidos en Linux/macOS. En Windows: Git Bash o WSL |

Ver instrucciones detalladas en [configuracion.md](configuracion.md).

## Paso 1 — Configurar credenciales AWS

```bash
aws configure --profile mcp-deploy
# Introduce: Access Key, Secret Key, us-east-2, json
```

## Paso 2 — Crear Key Pair EC2

```bash
aws --profile mcp-deploy ec2 create-key-pair \
    --key-name mcp-deploy-key \
    --query 'KeyMaterial' \
    --output text \
    --region us-east-2 > ~/.ssh/mcp-deploy-key.pem

chmod 400 ~/.ssh/mcp-deploy-key.pem
```

## Paso 3 — Configurar .env

```bash
cd MCP-DEPLOY
cp .env.example .env
nano .env   # Completa TODOS los campos requeridos
```

Variables mínimas requeridas:
- `TELEGRAM_BOT_TOKEN`
- `GITHUB_MIATECH_TOKEN` + `GITHUB_MIATECH_ORG`
- `GITHUB_AEROMEXICO_TOKEN` + `GITHUB_AEROMEXICO_ORG`
- `REPO_FACTURACION` + `REPO_RAM`
- `ALLOWED_TELEGRAM_USERS`

## Paso 4 — Ejecutar el deploy

```bash
cd MCP-DEPLOY/infra/scripts

# Opción A: Con variables de entorno
export KEY_PAIR_NAME=mcp-deploy-key
export KEY_FILE=~/.ssh/mcp-deploy-key.pem
export AWS_PROFILE=mcp-deploy
./deploy.sh

# Opción B: Con argumentos explícitos
./deploy.sh \
    -k mcp-deploy-key \
    -f ~/.ssh/mcp-deploy-key.pem \
    -p mcp-deploy \
    -r us-east-2

# Solo infraestructura (no despliega la app)
./deploy.sh -k mcp-deploy-key --infra-only

# Solo aplicación (infra ya existe)
./deploy.sh -f ~/.ssh/mcp-deploy-key.pem --app-only
```

El script hace todo automáticamente:
1. ✅ Verifica prerrequisitos
2. ✅ Despliega CloudFormation (2 instancias EC2)
3. ✅ Compila binarios Go para `linux/arm64`
4. ✅ Copia MCP server + script a EC2-MCP
5. ✅ Copia Bot a EC2-Bot con IP privada del MCP
6. ✅ Instala y arranca servicios systemd en ambas instancias

Duración aproximada: **8-12 minutos** (primera vez).

## Paso 5 — Verificar el despliegue

```bash
# Health check del MCP server (desde el Bot EC2 o con SSH)
ssh -i ~/.ssh/mcp-deploy-key.pem ec2-user@<BOT-IP>
curl http://<MCP-PRIVATE-IP>:8081/internal/health

# Logs del MCP server
./ssh-connect.sh --mcp -f ~/.ssh/mcp-deploy-key.pem
sudo journalctl -u mcp-server -f

# Logs del Bot
./ssh-connect.sh --bot -f ~/.ssh/mcp-deploy-key.pem
sudo journalctl -u telegram-bot -f
```

## Conectarse via SSH

```bash
cd MCP-DEPLOY/infra/scripts

# Conectar al Bot
./ssh-connect.sh --bot -f ~/.ssh/mcp-deploy-key.pem

# Conectar al MCP Server
./ssh-connect.sh --mcp -f ~/.ssh/mcp-deploy-key.pem
```

## Gestión de servicios (dentro de EC2)

```bash
# Ver estado
sudo systemctl status mcp-server
sudo systemctl status telegram-bot

# Reiniciar
sudo systemctl restart mcp-server
sudo systemctl restart telegram-bot

# Ver logs en vivo
sudo journalctl -u mcp-server -f
sudo journalctl -u telegram-bot -f

# Ver logs históricos
sudo journalctl -u mcp-server --since "1 hour ago"
```

## Actualizar la aplicación (re-deploy solo de la app)

```bash
# Hacer cambios en el código, luego:
cd MCP-DEPLOY/infra/scripts
./deploy.sh -f ~/.ssh/mcp-deploy-key.pem --app-only
```

## Eliminar toda la infraestructura

```bash
cd MCP-DEPLOY/infra/scripts
./teardown.sh -s mcp-deploy -r us-east-2 -p mcp-deploy
```

⚠️ Esto elimina **todos** los recursos AWS incluyendo las instancias EC2 y la Elastic IP.

## Costos estimados (us-east-2)

| Recurso | Costo/mes |
|---------|-----------|
| 2× t4g.micro | ~$12 |
| 1× Elastic IP (en uso) | $0 |
| 2× EBS gp3 10 GB | ~$1.60 |
| Tráfico de red | ~$0 |
| **Total estimado** | **~$13-15/mes** |
