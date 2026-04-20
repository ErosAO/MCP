# Plantilla: Como construyo mis MCP Servers

Este documento es mi referencia personal para crear nuevos proyectos MCP.
Captura las decisiones de arquitectura, infraestructura y patrones de codigo
que quiero reusar. Solo quedan abiertas tres variables por proyecto:

> **Variables por definir en cada proyecto**
> - `[LENGUAJE]` — lenguaje de programacion del servidor MCP
> - `[TOOLS]` — herramientas o capacidades especificas que expone
> - `[PROPOSITO]` — para que sirve el proyecto

---

## 1. Vision general

Cada proyecto MCP sigue esta estructura de dos interfaces sobre un nucleo compartido:

```
Cliente MCP (Claude Desktop / IDE)
        │
        ▼
┌─────────────────────┐
│    MCP Server       │  ← expone [TOOLS] via protocolo MCP
│   (SSE / stdio)     │
└────────┬────────────┘
         │  nucleo compartido
┌────────┴────────────┐
│   Logica de [TOOLS] │  ← implementacion pura, sin dependencias MCP
└────────┬────────────┘
         │
┌────────┴────────────┐
│   Telegram Bot      │  ← acceso en lenguaje natural via Claude Code CLI
└─────────────────────┘
```

**Principio clave:** El nucleo de logica (las tools) es independiente.
Ni el servidor MCP ni el bot lo poseen — ambos lo importan.
Esto garantiza comportamiento identico en las dos interfaces y evita duplicacion.

---

## 2. Estructura de carpetas

```
proyecto/
├── src/
│   ├── mcp_server/
│   │   ├── __init__.py
│   │   ├── __main__.py      ← entry point: python -m mcp_server [sse|stdio]
│   │   ├── server.py        ← FastMCP con @mcp.tool() decorators
│   │   ├── tools.py         ← logica pura de [TOOLS] (sin MCP, sin Telegram)
│   │   └── config.py        ← todas las variables de entorno en un solo lugar
│   └── telegram_bot/
│       ├── __init__.py
│       ├── __main__.py      ← entry point: python -m telegram_bot
│       └── bot.py           ← handlers, bucle agentico, llamadas a claude -p
├── infra/
│   ├── cloudformation/
│   │   └── stack.yml        ← VPC + EC2 ARM (Graviton2)
│   └── scripts/
│       ├── deploy.sh        ← despliega infra + app en un comando
│       ├── ssh-connect.sh   ← tunnel SSH al EC2
│       └── teardown.sh      ← destruye todo el stack
├── systemd/
│   ├── mcp-server.service   ← servicio persistente del MCP server
│   └── telegram-bot.service ← servicio persistente del bot
├── requirements.txt
├── .env.example             ← plantilla de configuracion
└── .env                     ← configuracion real (nunca en git)
```

---

## 3. MCP Server

### Framework
Siempre uso **FastMCP** (libreria `mcp`).

### Transportes soportados
| Modo | Uso |
|------|-----|
| `sse` | Produccion — HTTP persistente en `127.0.0.1:PORT` |
| `stdio` | Clientes MCP locales / testing |

### Patron de server.py

```python
from mcp.server.fastmcp import FastMCP
from . import tools as _tools
from .config import MCP_SERVER_HOST, MCP_SERVER_PORT

mcp = FastMCP(name="[NOMBRE] MCP Server")

@mcp.tool()
def mi_tool(param: str) -> str:
    """Descripcion clara de lo que hace."""
    return _tools.mi_tool(param)

def run(transport: str = "sse") -> None:
    if transport in ("sse", "streamable-http"):
        mcp.run(transport=transport, host=MCP_SERVER_HOST, port=MCP_SERVER_PORT)
    else:
        mcp.run()  # stdio
```

### Reglas de tools.py
- Funciones puras: entrada → salida, sin efectos secundarios en el servidor
- Siempre retornan `str` (el protocolo MCP lo requiere para texto)
- Incluir validacion de entradas al inicio de cada funcion
- Un `_safe_path()` o equivalente para validar acceso a recursos externos

---

## 4. Bot de Telegram

### Flujo agentico (bucle)

```
Usuario envia mensaje
        ↓
Construir prompt = SYSTEM_PROMPT + historial de conversacion
        ↓
subprocess: claude -p --dangerously-skip-permissions "<prompt>"
        ↓
¿Respuesta es JSON {"tool": "...", "args": {...}}?
  Si ──► ejecutar tool localmente ──► agregar resultado al contexto ──► repetir
  No ──► enviar respuesta de texto al usuario
         (max 8 iteraciones para evitar bucles infinitos)
```

### Comandos estandar de cada bot
Todos mis bots implementan al menos estos cuatro comandos:

| Comando | Funcion |
|---------|---------|
| `/start` | Bienvenida y descripcion del bot |
| `/help` | Lista de comandos y ejemplos de uso |
| `/clear` | Borra el historial de conversacion del usuario |
| `[comando especifico]` | Accion rapida propia del proposito del bot |

### SYSTEM_PROMPT
El prompt del sistema instruye a Claude a responder con JSON cuando necesita
una herramienta y con texto normal cuando ya tiene la respuesta.
Formato de llamada a tool que espera el bot:

```json
{"tool": "nombre_herramienta", "args": {"param1": "valor1"}}
```

### Seguridad del bot
- `ALLOWED_TELEGRAM_USERS`: lista de user IDs o @usernames autorizados
- Si esta vacia en produccion, el bot rechaza todos los mensajes no autorizados
- Siempre verificar autorizacion al inicio de cada handler

### Historial de conversacion
- Almacenado en memoria por usuario (se pierde al reiniciar)
- Limitado a `MAX_HISTORY_MESSAGES` (default 20)
- `/clear` resetea solo el historial del usuario que lo ejecuta

---

## 5. Configuracion (.env)

### Variables siempre presentes

```env
# Bot
TELEGRAM_BOT_TOKEN=          # REQUERIDO — de @BotFather
ALLOWED_TELEGRAM_USERS=      # IDs/usernames separados por coma

# Claude
CLAUDE_MODEL=claude-sonnet-4-6
CLAUDE_BIN=claude            # en EC2 usar /usr/local/bin/claude-run

# MCP Server
MCP_SERVER_HOST=127.0.0.1
MCP_SERVER_PORT=8000

# Limites
MAX_HISTORY_MESSAGES=20
```

### Variables especificas del proyecto
Cada proyecto agrega aqui las variables propias de [TOOLS] y [PROPOSITO].

### Resolucion del .env
El `config.py` busca en este orden:
1. `/opt/mcp/app/.env` (produccion EC2)
2. `<raiz-repo>/.env` (desarrollo)
3. `<cwd>/.env` (fallback)

---

## 6. Infraestructura AWS

### Stack CloudFormation
Un solo template `infra/cloudformation/stack.yml` crea todo:
- VPC + Subnet publica + Internet Gateway
- Security Group (solo SSH entrante + todo trafico saliente)
- EC2 instance ARM Graviton2 con Amazon Linux 2023

### Instancia EC2
| Parametro | Valor |
|-----------|-------|
| Arquitectura | ARM64 (Graviton2) |
| AMI | Amazon Linux 2023 ARM64 (resuelto via SSM automaticamente) |
| Tipo default | `t4g.micro` (1 GB RAM, ~$0.0084/hr) |
| Opciones | `t4g.nano` · `t4g.micro` · `t4g.small` |
| Region default | `us-east-2` |

### Layout en EC2
```
/opt/mcp/
├── app/     ← codigo fuente (git clone o scp)
├── files/   ← datos/recursos del proyecto
└── logs/    ← logs de aplicacion
```

### Servicios systemd
Dos servicios persistentes, se levantan automaticamente con la instancia:
- `mcp-server.service` → SSE en `127.0.0.1:8000`
- `telegram-bot.service` → polling a Telegram API

### Deploy en un comando
```bash
./infra/scripts/deploy.sh \
  -k <key-pair-name> \
  -f <ruta-al.pem> \
  -r us-east-2 \
  -t t4g.micro
```

El script hace en orden:
1. Valida prerrequisitos (AWS CLI, ssh, scp)
2. Despliega el stack CloudFormation
3. Espera a que SSH este disponible
4. Copia codigo fuente, .env y credenciales de Claude (`~/.claude/`)
5. Instala Node.js (nvm), Claude Code CLI y dependencias Python
6. Crea wrapper `/usr/local/bin/claude-run` para que systemd pueda invocar claude
7. Habilita los servicios systemd

### Paso manual: autenticacion de Claude en EC2
Si las credenciales locales no se copiaron correctamente:
```bash
ssh -i tu-key.pem ec2-user@<IP>
claude                        # login OAuth interactivo
claude -p "Responde solo: OK" # verificar que funciona
sudo systemctl start mcp-server telegram-bot
```

---

## 7. Seguridad: lista de verificacion

- [ ] `ALLOWED_TELEGRAM_USERS` configurado con usuarios reales
- [ ] `.env` nunca en git (esta en `.gitignore`)
- [ ] MCP server en `127.0.0.1` (no expuesto a internet)
- [ ] SSH CIDR restringido a mi IP (`1.2.3.4/32`) en produccion
- [ ] Validacion de rutas/recursos en `tools.py` para prevenir accesos no autorizados
- [ ] Limite de tamano en recursos que se leen (evitar OOM)

---

## 8. Lo que cambia en cada proyecto

| Elemento | Variable |
|----------|----------|
| Lenguaje del MCP server | `[LENGUAJE]` — Python es el default, pero puede ser otro |
| Herramientas expuestas | `[TOOLS]` — las funciones en `tools.py` y `server.py` |
| Proposito del bot | `[PROPOSITO]` — define el SYSTEM_PROMPT y los comandos custom |
| Variables de entorno adicionales | Segun recursos que necesite el proyecto |
| Nombre del stack CloudFormation | `STACK_NAME` en el deploy |

Todo lo demas (arquitectura, infraestructura, patrones de codigo, seguridad) se replica de este documento.
