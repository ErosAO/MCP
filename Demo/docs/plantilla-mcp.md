# Brief para construir un nuevo proyecto MCP

Cuando el usuario te diga el **lenguaje** y el **proposito**, construye el proyecto completo
siguiendo exactamente este documento. No preguntes como hacerlo — ya esta decidido aqui.
Solo necesitas derivar las herramientas (`tools`) y el `SYSTEM_PROMPT` del proposito que te den.

**Sobre el lenguaje:** usa el lenguaje que el usuario indique. Elige las librerias
idiomaticas de ese lenguaje para MCP y para Telegram. Adapta la estructura de archivos,
el sistema de dependencias y los comandos de ejecucion al ecosistema de ese lenguaje.
Los patrones de arquitectura y comportamiento son los mismos sin importar el lenguaje.

---

## Lo que el usuario te dira

```
Lenguaje : <lenguaje de programacion, ej: Go, Python, TypeScript>
Proposito: <para que sirve el MCP y el bot>
```

Con eso debes generar **todos** los archivos del proyecto sin pedir mas informacion.

---

## Lo que debes construir (siempre)

Genera estos modulos logicos adaptando nombres y estructura al lenguaje indicado:

```
[modulo mcp_server]
  config        ← variables de entorno centralizadas
  tools         ← logica pura de las herramientas (sin MCP, sin Telegram)
  server        ← registra cada tool en el servidor MCP
  main/entrypoint

[modulo telegram_bot]
  bot           ← handlers, bucle agentico, llamadas a claude -p
  main/entrypoint

infra/cloudformation/stack.yml
infra/scripts/deploy.sh
infra/scripts/ssh-connect.sh
infra/scripts/teardown.sh
systemd/mcp-server.service
systemd/telegram-bot.service
[archivo de dependencias del lenguaje]   ← requirements.txt / go.mod / package.json / etc.
.env.example
.gitignore
```

Usa las convenciones de estructura de ese lenguaje. Por ejemplo:
- **Go**: `cmd/`, `internal/`, `go.mod`, un binario compilado por componente
- **Python**: `src/`, modulos con `__init__.py`, `requirements.txt`
- **TypeScript**: `src/`, `package.json`, `tsconfig.json`

---

## Arquitectura (no negociable)

```
Cliente MCP (Claude Desktop / IDE)
        │
        ▼
  MCP Server (FastMCP, SSE o stdio)
  src/mcp_server/server.py
        │
        │  importa
        ▼
  Logica pura de herramientas       ← UNICA fuente de verdad
  src/mcp_server/tools.py
        │
        │  importa (tambien)
        ▼
  Telegram Bot (claude -p como backend)
  src/telegram_bot/bot.py
```

**Regla de oro:** `tools.py` no depende de MCP ni de Telegram.
`server.py` y `bot.py` la importan por separado. Nunca dupliques logica.

---

## Patron de cada modulo

### `config` (o equivalente en el lenguaje)
Lee todas las variables de entorno. Las expone como constantes o structs importables.
Busca `.env` en este orden:
1. `/opt/mcp/app/.env` (produccion EC2)
2. `<raiz-repo>/.env`
3. `<cwd>/.env`

Variables que **siempre** deben estar:
```
TELEGRAM_BOT_TOKEN        string   requerido
ALLOWED_TELEGRAM_USERS    []string separados por coma; vacio = todos denegados en prod
CLAUDE_MODEL              string   default "claude-sonnet-4-6"
CLAUDE_BIN                string   default "claude"
MCP_SERVER_HOST           string   default "127.0.0.1"
MCP_SERVER_PORT           int      default 8000
MAX_HISTORY_MESSAGES      int      default 20
```
Agrega las variables adicionales que el proposito requiera (API keys externas, rutas, limites, etc).

---

### `tools` (o equivalente en el lenguaje)
Funciones puras derivadas del proposito. Reglas independientes del lenguaje:
- Cada funcion recibe parametros tipados y retorna texto (`string`)
- Validar entradas al inicio; retornar mensajes de error descriptivos, no lanzar excepciones al caller
- Si accedes a archivos o rutas, incluye logica que impida path traversal (equivalente a `safe_path`)
- Si llamas APIs externas, captura errores de red y retornalos como string
- Sin estado global, sin efectos secundarios en el proceso servidor

Pseudocodigo de la estructura minima de cada tool:
```
funcion nombre_tool(param: string) -> string:
    si param esta vacio:
        retornar "Error: <descripcion>"
    intentar:
        // logica
        retornar "<resultado>"
    atrapar error:
        retornar "Error: " + error.mensaje
```

---

### `server` (o equivalente en el lenguaje)
Usa la libreria MCP idiomatica del lenguaje indicado para registrar cada funcion
de `tools` como herramienta MCP. Un registro por funcion.
El servidor debe soportar transporte SSE (produccion) y stdio (clientes locales).

Comportamiento esperado:
- Modo SSE: escucha en `MCP_SERVER_HOST:MCP_SERVER_PORT`
- Modo stdio: lee de stdin, escribe a stdout
- La descripcion de cada tool es lo que vera el cliente MCP — hazla clara y util

---

### `bot` (o equivalente en el lenguaje)
Bot de Telegram con bucle agentico. Estructura fija:

**SYSTEM_PROMPT:** Describe al asistente segun el proposito. Lista las tools disponibles
con su firma y descripcion. Instruye a Claude a responder con JSON exactamente asi
cuando necesite una tool:
```json
{"tool": "nombre_tool", "args": {"param1": "valor1"}}
```
Y con texto normal cuando ya tiene la respuesta final.

**Comandos que siempre existen:**
| Comando | Comportamiento |
|---------|---------------|
| `/start` | Bienvenida personalizada al proposito del bot |
| `/help` | Lista comandos + 5 ejemplos de uso en lenguaje natural |
| `/clear` | Borra historial del usuario |
| `[1 comando especifico]` | Accion rapida relevante al proposito (tu lo defines) |

**Bucle agentico (no cambiar esta logica):**
```
mensaje usuario
    → construir prompt (SYSTEM_PROMPT + historial[-MAX_HISTORY_MESSAGES:])
    → claude -p --dangerously-skip-permissions "<prompt>"
    → si respuesta es JSON valido con "tool" y "args":
          ejecutar TOOL_MAP[tool](args)
          agregar resultado al contexto
          repetir (max 8 iteraciones)
    → si es texto: enviar al usuario, guardar en historial, terminar
```

**Autorizacion:** verificar `_is_authorized(user_id, username)` al inicio de cada handler.
Si `ALLOWED_TELEGRAM_USERS` esta vacio, denegar por defecto en produccion.

**Mensajes largos:** si la respuesta supera 4000 caracteres, dividir en partes.

---

### Entrypoints
Cada componente (mcp_server y telegram_bot) tiene su propio entrypoint ejecutable.
Usa las convenciones del lenguaje (funcion `main`, binario compilado, script, etc).
El mcp_server acepta un argumento `sse` o `stdio` para seleccionar el transporte.

---

## Infraestructura AWS (siempre igual)

### `infra/cloudformation/stack.yml`
Crea exactamente estos recursos:
- VPC con CIDR `10.0.0.0/16`
- Subnet publica en la primera AZ disponible
- Internet Gateway + Route Table
- Security Group: SSH entrante (parametrizable por CIDR) + todo trafico saliente
- EC2 ARM64 Graviton2 con Amazon Linux 2023 (AMI via SSM Parameter Store)
- Elastic IP asociada a la instancia
- Volumen EBS gp3, tamano parametrizable (8-30 GB, default 10)

Parametros CloudFormation:
- `KeyPairName` — requerido
- `InstanceType` — valores: `t4g.nano`, `t4g.micro` (default), `t4g.small`
- `SSHAllowedCidr` — default `0.0.0.0/0`
- `ProjectName` — usado para nombrar recursos
- `VolumeSize` — default 10
- `LatestAmiId` — tipo `AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>`, resuelve automaticamente

Outputs requeridos: `InstancePublicIP`, `InstanceId`.

### Servicios systemd

`systemd/mcp-server.service`:
- Ejecuta `python -m mcp_server sse`
- Usuario `ec2-user`
- WorkingDirectory `/opt/mcp/app`
- Restart `on-failure`, RestartSec `5s`
- `After=network.target`

`systemd/telegram-bot.service`:
- Ejecuta `python -m telegram_bot`
- Misma configuracion base
- `After=network.target mcp-server.service`

### `infra/scripts/deploy.sh`
Script bash que hace en orden:
1. Validar prerequisitos: `aws`, `ssh`, `scp` en PATH y credenciales AWS activas
2. `aws cloudformation deploy` con el template y los parametros
3. Obtener IP del output `InstancePublicIP`
4. Esperar SSH (poll cada 10s, max 6 min)
5. `scp` del codigo fuente, `requirements.txt` y `.env` a `/opt/mcp/app/`
6. `scp` de archivos systemd a `/tmp/systemd_units/`
7. Si existe `~/.claude/.claude.json`, copiarlo a `~/.claude/` en EC2
8. SSH: instalar nvm → Node.js LTS → `@anthropic-ai/claude-code`
9. SSH: crear wrapper `/usr/local/bin/claude-run` que carga nvm antes de invocar `claude`
10. SSH: `pip install -r requirements.txt`
11. SSH: copiar `.service` files a `/etc/systemd/system/`, `daemon-reload`, `enable` ambos servicios
12. Mostrar IP y siguientes pasos (autenticar Claude manualmente si es necesario)

Flags del script: `-k/--key-pair`, `-f/--key-file`, `-s/--stack`, `-r/--region`,
`-t/--type`, `--cidr`, `--infra-only`, `--app-only`, `-y/--yes`, `-h/--help`.

### `infra/scripts/teardown.sh`
`aws cloudformation delete-stack` con confirmacion previa.

### `infra/scripts/ssh-connect.sh`
Obtiene IP del stack, abre tunel SSH con reenvio del puerto 8000.

---

## `.env.example`
Incluye todas las variables con comentarios explicativos.
Las variables especificas del proposito van al final, en su propia seccion comentada.
Nunca incluyas valores reales, solo placeholders o defaults.

```env
TELEGRAM_BOT_TOKEN=1234567890:AAxxxxxxxx
ALLOWED_TELEGRAM_USERS=
CLAUDE_MODEL=claude-sonnet-4-6
CLAUDE_BIN=claude
MCP_SERVER_HOST=127.0.0.1
MCP_SERVER_PORT=8000
MAX_HISTORY_MESSAGES=20
# --- Variables especificas de este proyecto ---
# VARIABLE_PROPIA=valor_ejemplo
```

---

## Dependencias
Usa el gestor de dependencias del lenguaje indicado (`go.mod`, `requirements.txt`,
`package.json`, etc). Las dependencias minimas son:

- Libreria MCP para el lenguaje (busca la oficial o la mas adoptada)
- Libreria de Telegram Bot para el lenguaje
- Libreria para leer `.env` / variables de entorno
- HTTP client si el proposito requiere llamar APIs externas

Agrega las dependencias adicionales que el proposito requiera.

---

## `.gitignore`
```
.env
__pycache__/
*.pyc
.venv/
*.pem
logs/
```

---

## Seguridad: aplica siempre sin que te lo pidan

- `ALLOWED_TELEGRAM_USERS` vacio significa acceso denegado en produccion (no abierto)
- MCP server escucha en `127.0.0.1`, nunca en `0.0.0.0` por default
- Si el proposito implica acceso a archivos o rutas, implementa `_safe_path()` en `tools.py`
- Si el proposito implica credenciales externas, estas van en `.env`, nunca en el codigo
- El `.env` nunca va al repositorio

---

## Lo unico que cambia entre proyectos

| Que | Como lo derivas |
|-----|----------------|
| **Lenguaje** | El usuario lo dice. Adapta estructura, dependencias y comandos al ecosistema |
| Funciones en `tools` | Del proposito: que acciones tiene sentido automatizar |
| Registros en `server` | Uno por cada funcion de `tools` |
| `SYSTEM_PROMPT` en `bot` | Del proposito: como debe presentarse el asistente y que puede hacer |
| Comando especifico del bot | La accion mas comun del proposito como atajo rapido |
| Variables extra en `config` y `.env.example` | Lo que las tools necesiten (API keys, URLs, rutas) |
| Dependencias | Las librerias propias del lenguaje y del proposito |
| Servicios systemd `ExecStart` | El comando de arranque segun el lenguaje (binario compilado, script, etc) |

Todo lo demas — arquitectura, infraestructura, patrones de comportamiento, seguridad, despliegue — es identico en todos los proyectos y esta definido en este documento.
