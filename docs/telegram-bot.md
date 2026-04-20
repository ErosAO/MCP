# Bot de Telegram

El bot de Telegram proporciona acceso al sistema de archivos mediante lenguaje natural, usando Claude Code CLI como backend de IA.

## Comandos disponibles

| Comando | Descripcion |
|---------|-------------|
| `/start` | Mensaje de bienvenida e instrucciones basicas |
| `/help` | Lista de comandos y ejemplos de uso |
| `/files` | Lista todos los archivos en el servidor |
| `/clear` | Borra el historial de conversacion actual |

## Ejemplos de uso en lenguaje natural

```
"Lee el archivo notas.txt"
"Crea un archivo llamado tareas.txt con mis pendientes"
"Resume el contenido de reporte.txt"
"Busca la palabra presupuesto en todos los archivos"
"¿Cuándo fue modificado datos.txt?"
"Elimina el archivo borrador.txt"
"Escribe un poema y guardalo en poema.txt"
"Lista los archivos de la carpeta docs"
```

## Flujo agentico

El bot implementa un bucle agentico para resolver solicitudes que requieren multiples pasos:

```
Mensaje usuario
      ↓
Construir prompt (SYSTEM_PROMPT + historial)
      ↓
claude -p <prompt>
      ↓
¿Respuesta es JSON {"tool": ..., "args": ...}?
  Si ──► Ejecutar herramienta ──► Agregar resultado al contexto ──► (repetir, max 8)
  No ──► Enviar texto al usuario
```

El limite de 8 iteraciones evita bucles infinitos en solicitudes complejas.

## Autorizacion de usuarios

Configura `ALLOWED_TELEGRAM_USERS` en `.env`:

```env
# Por user ID numerico o @username (o ambos)
ALLOWED_TELEGRAM_USERS=123456789,@mi_usuario,987654321
```

- Si la variable esta **vacia**, cualquier usuario puede interactuar con el bot (no recomendado en produccion).
- Para obtener tu user ID habla con `@userinfobot` en Telegram.

## Historial de conversacion

- El bot mantiene contexto de conversacion por usuario (almacenado en memoria del proceso).
- El historial se limita a `MAX_HISTORY_MESSAGES` mensajes (default 20).
- El comando `/clear` resetea el historial del usuario actual.
- Al reiniciar el bot, todos los historiales se pierden.

## Limites de Telegram

Los mensajes de Telegram tienen un limite de 4096 caracteres. Si la respuesta de Claude es mas larga, el bot la divide automaticamente en partes de 4000 caracteres.

## Dependencia de Claude Code CLI

El bot usa `claude -p --dangerously-skip-permissions` via `subprocess`. Requiere:
1. Claude Code CLI instalado (`npm install -g @anthropic-ai/claude-code`)
2. Sesion autenticada en `~/.claude/` (ejecutar `claude` una vez para hacer login)

La variable `CLAUDE_BIN` permite especificar la ruta exacta al binario (util en produccion EC2 donde se usa un wrapper `/usr/local/bin/claude-run`).

## Logs

Los logs del bot se escriben en dos lugares:
- **stdout**: visible en `journalctl` (produccion) o consola (desarrollo)
- **Archivo**: `LOGS_DIR/telegram-bot.log` (default: `./logs/` o `/opt/mcp/logs/` en EC2)

Nivel de log: `INFO`. Incluye user ID, username y primeros 120 caracteres de cada mensaje.
