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
aws configure --profile mcp-demo
# Introduce: Access Key, Secret Key, us-east-2, json
```

## Paso 2 — Crear Key Pair EC2

```bash
aws --profile mcp-demo ec2 create-key-pair \
    --key-name key_pair_mcp_demo \
    --query 'KeyMaterial' \
    --output text \
    --region us-east-2 > ~/.ssh/key_pair_mcp_demo.pem

chmod 400 ~/.ssh/key_pair_mcp_demo.pem
```

## Paso 3 — Configurar .env

```bash
cd MCP-DEPLOY
cp .env.example .env
nano .env   # Completa TODOS los campos requeridos
```

Variables mínimas requeridas:

| Variable | Descripción |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Token del bot de @BotFather |
| `ALLOWED_TELEGRAM_USERS` | IDs Telegram autorizados (separados por coma) |
| `GITHUB_MIATECH_TOKEN` | PAT de la cuenta Miatech (permisos: `repo`, `read:org`) |
| `GITHUB_AEROMEXICO_TOKEN` | PAT de la cuenta Aeromexico (permisos: `repo`, `workflow`, `read:org`) |

## Paso 4 — Ejecutar el deploy

```bash
cd MCP-DEPLOY/infra/scripts

# Con los defaults del proyecto (perfil mcp-demo, key key_pair_mcp_demo)
./deploy.sh

# Con argumentos explícitos
./deploy.sh \
    -k key_pair_mcp_demo \
    -f ~/.ssh/key_pair_mcp_demo.pem \
    -p mcp-demo \
    -r us-east-2

# Solo infraestructura (CloudFormation, sin copiar binarios)
./deploy.sh -k key_pair_mcp_demo --infra-only

# Solo aplicación (la infra ya existe)
./deploy.sh --app-only
```

El script hace todo automáticamente:
1. ✅ Verifica prerrequisitos (aws, go, gh, ssh)
2. ✅ Despliega CloudFormation (2 instancias EC2 ARM t4g.micro)
3. ✅ Compila binarios Go para `linux/arm64`
4. ✅ Despliega MCP server (binario + repos GitHub + servicio systemd)
5. ✅ Despliega Bot Telegram (binario + Node.js + claude-run + servicio systemd)

Duración aproximada: **8-12 minutos** (primera vez).

## Paso 5 — Autenticar Claude en el Bot EC2

El deploy instala Claude Code automáticamente, pero necesitas autenticarte manualmente la primera vez. Si ya tienes credenciales locales en `~/.claude/.claude.json`, el script las copia automáticamente.

Si no:

```bash
# Conectar al Bot EC2
ssh -i ~/.ssh/key_pair_mcp_demo.pem ec2-user@<BOT-IP>

# Autenticar (abre una URL en el navegador)
claude

# Una vez autenticado, reiniciar el servicio
sudo systemctl restart telegram-bot
sudo systemctl status telegram-bot
```

## Paso 6 — Verificar el despliegue

```bash
# Conectar al Bot
ssh -i ~/.ssh/key_pair_mcp_demo.pem ec2-user@<BOT-IP>

# Conectar al MCP Server
ssh -i ~/.ssh/key_pair_mcp_demo.pem ec2-user@<MCP-PUBLIC-IP>

# Health check del MCP server (ejecutar desde el Bot o con SSH al MCP)
curl http://<MCP-PRIVATE-IP>:8081/internal/health
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

## Verificar repositorios clonados (MCP Server)

```bash
ssh -i ~/.ssh/key_pair_mcp_demo.pem ec2-user@<MCP-PUBLIC-IP>

# Ver todos los repos
find ~/repos -maxdepth 2 -name '.git' -type d | sed 's|/.git||' | sort

# Estructura esperada:
# ~/repos/FEBOL/miatech/am-fe-mx-api
# ~/repos/FEBOL/miatech/mi-fe-mx-front
# ~/repos/FEBOL/miatech/mi-fe-mx-front-individual
# ~/repos/FEBOL/am/FEBOL_MX_Backend_Read
# ~/repos/FEBOL/am/FEBOL_MX_Backend_CronJobs
# ~/repos/FEBOL/am/FEBOL_MX_Backend_Timbrado
# ~/repos/FEBOL/am/FEBOL_MX_Backend_Input
# ~/repos/FEBOL/am/FEBOL_MX_Backend_Write
# ~/repos/FEBOL/am/FEBOL_MX_Backend_IRead
# ~/repos/FEBOL/am/FEBOL_MX_Backend_IWrite
# ~/repos/FEBOL/am/FEBOL_MX_Portal_Individual
# ~/repos/FEBOL/am/FEBOL_MX_Portal
# ~/repos/FEBOL/am-febol-devops/
```

## Actualizar la aplicación (re-deploy solo de la app)

```bash
# Hacer cambios en el código, luego:
cd MCP-DEPLOY/infra/scripts
./deploy.sh --app-only
```

## Eliminar toda la infraestructura

```bash
cd MCP-DEPLOY/infra/scripts
./teardown.sh -s mcp-deploy -r us-east-2 -p mcp-demo
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

## Troubleshooting

### telegram-bot.service falla al iniciar

```bash
sudo journalctl -u telegram-bot -n 50 --no-pager
```

Causas comunes:
- **Claude no autenticado** → `claude` en el Bot EC2 y reiniciar
- **`.env` mal configurado** → revisar `TELEGRAM_BOT_TOKEN` y `MCP_SERVER_HOST`
- **MCP server no accesible** → verificar que `mcp-server.service` esté activo

### Repos de Aeromexico no clonados (directorio `am` vacío)

Si el token de AM no tiene permisos GraphQL, clonar manualmente:

```bash
ssh -i ~/.ssh/key_pair_mcp_demo.pem ec2-user@<MCP-PUBLIC-IP>
AMX_TOKEN=$(grep GITHUB_AEROMEXICO_TOKEN /opt/mcp-deploy/app/.env | cut -d= -f2-)
cd ~/repos/FEBOL/am

for repo in FEBOL_MX_Backend_Read FEBOL_MX_Backend_CronJobs FEBOL_MX_Backend_Timbrado \
            FEBOL_MX_Backend_Input FEBOL_MX_Backend_Write FEBOL_MX_Backend_IRead \
            FEBOL_MX_Backend_IWrite FEBOL_MX_Portal_Individual FEBOL_MX_Portal; do
    git clone "https://x-access-token:${AMX_TOKEN}@github.com/BO-AMX/${repo}.git"
done
```
