# Arquitectura del Sistema

## Vision general

El proyecto tiene dos interfaces independientes que comparten el mismo nucleo de herramientas de archivos:

```
┌─────────────────────────────────────────────────────────────┐
│                        CLIENTES                             │
│                                                             │
│  Claude Desktop / IDE  ◄──►  MCP Server (FastMCP, SSE)     │
│                               src/mcp_server/server.py      │
│                                      │                      │
│  Usuario Telegram ◄──► Bot ◄──► claude -p (CLI)            │
│                         src/telegram_bot/bot.py             │
│                                      │                      │
│                              ┌───────┴────────┐            │
│                              │  tools.py       │            │
│                              │  (logica pura)  │            │
│                              └───────┬────────┘            │
│                                      │                      │
│                              FILES_DIR (disco)              │
└─────────────────────────────────────────────────────────────┘
```

## Componentes

### 1. MCP Server (`src/mcp_server/`)

| Archivo | Responsabilidad |
|---------|----------------|
| `server.py` | Define las 6 herramientas MCP con `@mcp.tool()` usando FastMCP |
| `tools.py` | Implementacion pura de cada herramienta (sin dependencias MCP) |
| `config.py` | Lee variables de entorno y resuelve rutas segun entorno |
| `__main__.py` | Entry point: `python -m mcp_server [sse\|stdio]` |

El servidor puede ejecutarse con dos transportes:
- **SSE** (Server-Sent Events): HTTP persistente en `127.0.0.1:8000`, ideal para produccion
- **stdio**: Comunicacion por stdin/stdout, para clientes MCP locales

### 2. Telegram Bot (`src/telegram_bot/`)

| Archivo | Responsabilidad |
|---------|----------------|
| `bot.py` | Handlers de Telegram, bucle agentico, llamadas a `claude -p` |
| `__main__.py` | Entry point: `python -m telegram_bot` |

El bot importa `tools.py` directamente (no pasa por el servidor MCP). Usa `claude -p` como backend de IA mediante un bucle agentico de hasta 8 iteraciones.

### 3. Nucleo compartido

`tools.py` es la unica fuente de verdad para la logica de archivos. Ambos componentes la importan, garantizando comportamiento identico sin duplicacion.

## Flujo del Bot de Telegram

```
1. Usuario envia mensaje
2. Bot construye prompt (historial + SYSTEM_PROMPT)
3. claude -p ejecuta el prompt
4. Si Claude responde JSON {"tool": "...", "args": {...}}:
     └─► Bot ejecuta la herramienta localmente
     └─► Agrega resultado al contexto
     └─► Vuelve al paso 3 (max 8 iteraciones)
5. Si Claude responde texto normal:
     └─► Bot envia la respuesta al usuario
```

## Seguridad

- **Sandboxing**: `_safe_path()` verifica que toda ruta resuelva dentro de `FILES_DIR`. Previene path traversal.
- **Autorizacion Telegram**: `ALLOWED_TELEGRAM_USERS` acepta user IDs numericos o @usernames. Si esta vacia, todos pueden usar el bot.
- **Red**: El servidor MCP escucha en `127.0.0.1` por defecto (no expuesto a internet).
- **Tamano de archivo**: Limite configurable via `MAX_FILE_SIZE_MB` (default 10 MB).

## Infraestructura en produccion

```
AWS us-east-2
  └─ VPC
      └─ Subnet publica
          └─ EC2 t4g.micro (ARM Graviton2, Amazon Linux 2023)
              ├─ systemd: mcp-server.service   → SSE en 127.0.0.1:8000
              ├─ systemd: telegram-bot.service → polling a Telegram API
              └─ /opt/mcp/
                  ├─ app/     (codigo fuente)
                  ├─ files/   (archivos gestionados)
                  └─ logs/    (logs de aplicacion)
```
