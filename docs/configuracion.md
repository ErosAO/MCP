# Configuracion

Toda la configuracion se gestiona mediante un archivo `.env` en la raiz del repositorio.

## Inicio rapido

```bash
cp .env.example .env
# Edita .env con tus valores reales
```

## Variables de entorno

### Autenticacion

| Variable | Requerida | Default | Descripcion |
|----------|-----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | **Si** | — | Token del bot obtenido de @BotFather en Telegram |
| `ANTHROPIC_API_KEY` | No | — | Solo si usas el SDK de Anthropic directamente. Con `claude -p` no es necesaria |

### Modelo Claude

| Variable | Default | Opciones |
|----------|---------|---------|
| `CLAUDE_MODEL` | `claude-sonnet-4-6` | `claude-sonnet-4-6` · `claude-opus-4-6` · `claude-haiku-4-5-20251001` |

### Servidor MCP

| Variable | Default | Descripcion |
|----------|---------|-------------|
| `MCP_SERVER_HOST` | `127.0.0.1` | Host donde escucha el servidor. Usar `0.0.0.0` solo con firewall configurado |
| `MCP_SERVER_PORT` | `8000` | Puerto del servidor MCP (SSE) |

### Rutas

| Variable | Default dev | Default EC2 | Descripcion |
|----------|-------------|-------------|-------------|
| `FILES_DIR` | `./files` | `/opt/mcp/files` | Directorio raiz donde se almacenan los archivos gestionados |

El directorio se crea automaticamente si no existe.

### Seguridad del bot

| Variable | Default | Descripcion |
|----------|---------|-------------|
| `ALLOWED_TELEGRAM_USERS` | _(vacio)_ | Lista separada por comas de user IDs o @usernames autorizados. Vacio = todos permitidos |

Ejemplo: `ALLOWED_TELEGRAM_USERS=123456789,987654321,@mi_usuario`

Para obtener tu user ID de Telegram habla con `@userinfobot`.

### Limites

| Variable | Default | Descripcion |
|----------|---------|-------------|
| `MAX_FILE_SIZE_MB` | `10` | Tamano maximo de archivo para lectura (MB) |
| `MAX_HISTORY_MESSAGES` | `20` | Numero maximo de mensajes que se mantienen en el historial de conversacion del bot |

### Binario de Claude (avanzado)

| Variable | Default | Descripcion |
|----------|---------|-------------|
| `CLAUDE_BIN` | `claude` | Ruta al binario de Claude Code. En produccion EC2 se usa `/usr/local/bin/claude-run` |

## Resolucion del archivo .env

El sistema busca `.env` en este orden y carga el primero que encuentra:

1. `/opt/mcp/app/.env` (produccion EC2)
2. `<raiz-del-repo>/.env` (desarrollo)
3. `<directorio-actual>/.env` (fallback)

## Ejemplo de .env completo

```env
TELEGRAM_BOT_TOKEN=1234567890:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
CLAUDE_MODEL=claude-sonnet-4-6
ALLOWED_TELEGRAM_USERS=123456789,@mi_usuario
FILES_DIR=/opt/mcp/files
MCP_SERVER_HOST=127.0.0.1
MCP_SERVER_PORT=8000
MAX_FILE_SIZE_MB=10
MAX_HISTORY_MESSAGES=20
```
