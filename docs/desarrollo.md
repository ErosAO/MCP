# Desarrollo Local

## Requisitos previos

- Python 3.11+
- Claude Code CLI: `npm install -g @anthropic-ai/claude-code`
- Sesion de Claude autenticada: ejecutar `claude` una vez para hacer login OAuth

## Instalacion

```bash
# Clonar el repositorio
git clone <repo-url>
cd MCP

# Crear entorno virtual
python3.11 -m venv .venv
source .venv/bin/activate

# Instalar dependencias
pip install -r requirements.txt

# Configurar variables de entorno
cp .env.example .env
# Editar .env con TELEGRAM_BOT_TOKEN y otros valores
```

## Ejecutar el MCP Server

```bash
# Modo SSE (HTTP en 127.0.0.1:8000)
python -m src.mcp_server sse

# Modo stdio (para clientes MCP locales)
python -m src.mcp_server stdio
```

El directorio `./files/` se crea automaticamente como `FILES_DIR` en desarrollo.

## Ejecutar el Bot de Telegram

```bash
python -m src.telegram_bot
```

Requiere `TELEGRAM_BOT_TOKEN` configurado en `.env`.

## Conectar Claude Desktop al servidor MCP local

Agrega esto a la configuracion de Claude Desktop (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "file-server": {
      "url": "http://127.0.0.1:8000/sse"
    }
  }
}
```

## Estructura de archivos en desarrollo

```
MCP/
├── src/
│   ├── mcp_server/
│   │   ├── __init__.py
│   │   ├── __main__.py   ← python -m mcp_server [sse|stdio]
│   │   ├── server.py     ← FastMCP con @mcp.tool()
│   │   ├── tools.py      ← logica de herramientas
│   │   └── config.py     ← variables de entorno
│   └── telegram_bot/
│       ├── __init__.py
│       ├── __main__.py   ← python -m telegram_bot
│       └── bot.py        ← handlers y bucle agentico
├── files/                ← FILES_DIR en desarrollo (auto-creado)
├── logs/                 ← logs en desarrollo (auto-creado)
├── .env                  ← tu configuracion local
├── .env.example          ← plantilla
└── requirements.txt
```

## Dependencias principales

| Paquete | Version | Uso |
|---------|---------|-----|
| `mcp[cli]` | >=1.0.0 | Protocolo MCP (FastMCP) |
| `python-telegram-bot` | >=20.7 | Bot de Telegram |
| `uvicorn` | >=0.30.0 | Servidor ASGI para SSE |
| `python-dotenv` | >=1.0.0 | Carga de .env |
| `httpx` | >=0.27.0 | Cliente HTTP async |

## Verificar que todo funciona

```bash
# Verificar que claude esta disponible
claude --version
claude -p "Responde solo: OK"

# Listar herramientas del servidor MCP (con el servidor corriendo)
# Conectar Claude Desktop o usar un cliente MCP

# Probar el bot
# Envia /start a tu bot en Telegram
```
