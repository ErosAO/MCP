"""
Bot de Telegram con Claude Code como backend de IA y herramientas MCP de archivos.

Flujo:
  Usuario → Telegram → Bot → claude -p (subprocess) → Tool calls → Respuesta → Telegram

No requiere ANTHROPIC_API_KEY. Usa las credenciales OAuth de Claude Code
almacenadas en ~/.claude/ (se copian desde la máquina local al EC2).
"""
import json
import logging
import subprocess
import sys
from pathlib import Path

from telegram import Update
from telegram.constants import ParseMode
from telegram.ext import (
    Application,
    CommandHandler,
    ContextTypes,
    MessageHandler,
    filters,
)

# Soporte para ejecución desde el repo (desarrollo) o como módulo instalado
_src_dir = Path(__file__).parent.parent
if str(_src_dir) not in sys.path:
    sys.path.insert(0, str(_src_dir))

from mcp_server.config import (
    ALLOWED_TELEGRAM_USERS,
    CLAUDE_MODEL,
    LOGS_DIR,
    MAX_HISTORY_MESSAGES,
    TELEGRAM_BOT_TOKEN,
)
from mcp_server.tools import (
    delete_file,
    get_file_info,
    list_files,
    read_file,
    search_in_files,
    write_file,
)

# ==============================================================
# Logging
# ==============================================================
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    handlers=[
        logging.StreamHandler(sys.stdout),
        logging.FileHandler(str(LOGS_DIR / "telegram-bot.log"), encoding="utf-8"),
    ],
)
logger = logging.getLogger(__name__)

# ==============================================================
# Mapa de herramientas disponibles
# ==============================================================
TOOL_MAP: dict = {
    "read_file":       lambda args: read_file(**args),
    "write_file":      lambda args: write_file(**args),
    "list_files":      lambda args: list_files(**args),
    "delete_file":     lambda args: delete_file(**args),
    "search_in_files": lambda args: search_in_files(**args),
    "get_file_info":   lambda args: get_file_info(**args),
}

# ==============================================================
# Prompt del sistema
# El prompt instruye a Claude a responder con JSON cuando necesite
# una herramienta, y con texto normal cuando ya tiene la respuesta.
# ==============================================================
SYSTEM_PROMPT = """Eres un asistente inteligente con acceso a un sistema de archivos de texto en el servidor.

HERRAMIENTAS DISPONIBLES:
- read_file(filename): Lee el contenido de un archivo de texto
- write_file(filename, content): Crea o sobreescribe un archivo de texto
- list_files(directory=""): Lista archivos y directorios (directory vacío = raíz)
- delete_file(filename): Elimina un archivo o directorio
- search_in_files(query, directory=""): Busca texto en los archivos
- get_file_info(filename): Información del archivo (tamaño, fechas)

REGLA IMPORTANTE PARA USAR HERRAMIENTAS:
Cuando necesites usar una herramienta, responde EXACTAMENTE con este formato JSON y NADA MÁS:
{"tool": "nombre_herramienta", "args": {"param1": "valor1", "param2": "valor2"}}

Ejemplos:
{"tool": "list_files", "args": {}}
{"tool": "read_file", "args": {"filename": "notas.txt"}}
{"tool": "write_file", "args": {"filename": "resumen.txt", "content": "El texto del resumen..."}}
{"tool": "search_in_files", "args": {"query": "presupuesto"}}

Cuando ya tienes toda la información para responder al usuario, responde en texto normal.
Sé conciso y útil. Responde siempre en el mismo idioma que el usuario."""


def _build_prompt(messages: list[dict]) -> str:
    """Construye el prompt completo para `claude -p` con el historial de conversación."""
    lines = [SYSTEM_PROMPT, "\n--- CONVERSACIÓN ---\n"]

    for msg in messages:
        role = msg["role"]
        content = msg["content"]

        if role == "user":
            if isinstance(content, str):
                lines.append(f"Usuario: {content}")
            else:
                # Resultado de herramienta
                lines.append(f"Sistema: {content}")
        elif role == "assistant":
            lines.append(f"Asistente: {content}")
        elif role == "tool_result":
            lines.append(f"Resultado de herramienta: {content}")

    lines.append("\nAsistente:")
    return "\n".join(lines)


import os as _os
CLAUDE_BIN = _os.getenv("CLAUDE_BIN", "claude")


def _call_claude(prompt: str, timeout: int = 120) -> str:
    """
    Llama a `claude -p` con el prompt dado.
    Usa las credenciales OAuth almacenadas en ~/.claude/ (no necesita API key).
    CLAUDE_BIN puede apuntar a la ruta absoluta del binario.
    """
    result = subprocess.run(
        [CLAUDE_BIN, "-p", "--dangerously-skip-permissions", prompt],
        capture_output=True,
        text=True,
        timeout=timeout,
    )

    if result.returncode != 0:
        error = result.stderr.strip() or "Error desconocido"
        raise RuntimeError(f"claude -p falló (código {result.returncode}): {error}")

    return result.stdout.strip()


def _try_parse_tool_call(text: str) -> dict | None:
    """
    Intenta parsear la respuesta de Claude como una llamada a herramienta.
    Devuelve el dict si es válido, None si es respuesta de texto normal.
    """
    text = text.strip()
    # Buscar JSON en la respuesta (puede haber texto extra antes/después)
    start = text.find("{")
    end = text.rfind("}") + 1
    if start == -1 or end == 0:
        return None
    try:
        data = json.loads(text[start:end])
        if "tool" in data and isinstance(data.get("args"), dict):
            return data
    except json.JSONDecodeError:
        pass
    return None


# ==============================================================
# Autorización
# ==============================================================
def _is_authorized(user_id: int, username: str | None) -> bool:
    if not ALLOWED_TELEGRAM_USERS:
        return True
    return (
        str(user_id) in ALLOWED_TELEGRAM_USERS
        or (username and username in ALLOWED_TELEGRAM_USERS)
    )


async def _deny(update: Update) -> None:
    await update.message.reply_text(
        "Lo siento, no estás autorizado para usar este bot.\n"
        "Contacta al administrador."
    )


# ==============================================================
# Handlers de comandos
# ==============================================================
async def cmd_start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    user = update.effective_user
    if not _is_authorized(user.id, user.username):
        await _deny(update)
        return

    await update.message.reply_text(
        f"¡Hola, {user.first_name}! 👋\n\n"
        "Soy tu asistente de archivos con Claude AI.\n\n"
        "Puedo ayudarte a:\n"
        "• 📖 Leer archivos de texto\n"
        "• ✍️ Crear y escribir archivos\n"
        "• 📋 Listar archivos disponibles\n"
        "• 🔍 Buscar texto en archivos\n"
        "• 📝 Resumir contenido de archivos\n"
        "• 🗑️ Eliminar archivos\n\n"
        "Escríbeme en lenguaje natural lo que necesitas.\n"
        "Usa /help para ver más opciones."
    )


async def cmd_help(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    user = update.effective_user
    if not _is_authorized(user.id, user.username):
        await _deny(update)
        return

    await update.message.reply_text(
        "*Comandos disponibles:*\n\n"
        "/start — Mensaje de bienvenida\n"
        "/help — Esta ayuda\n"
        "/files — Listar todos los archivos\n"
        "/clear — Borrar historial de conversación\n\n"
        "*Ejemplos de uso:*\n"
        "• `Lee el archivo notas.txt`\n"
        "• `Crea un archivo llamado tareas.txt`\n"
        "• `Resume el contenido de reporte.txt`\n"
        "• `Busca la palabra presupuesto en todos los archivos`\n"
        "• `¿Cuándo fue modificado datos.txt?`\n"
        "• `Elimina el archivo borrador.txt`\n"
        "• `Escribe un poema y guárdalo en poema.txt`",
        parse_mode=ParseMode.MARKDOWN,
    )


async def cmd_files(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    user = update.effective_user
    if not _is_authorized(user.id, user.username):
        await _deny(update)
        return

    result = list_files()
    await update.message.reply_text(
        f"*Archivos en el servidor:*\n```\n{result}\n```",
        parse_mode=ParseMode.MARKDOWN,
    )


async def cmd_clear(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    context.user_data["history"] = []
    await update.message.reply_text("✓ Historial de conversación borrado.")


# ==============================================================
# Handler principal de mensajes (bucle agéntico con Claude Code)
# ==============================================================
async def handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    user = update.effective_user
    if not _is_authorized(user.id, user.username):
        await _deny(update)
        return

    user_text = update.message.text
    logger.info("User %s (%s): %s", user.id, user.username, user_text[:120])

    await context.bot.send_chat_action(chat_id=update.effective_chat.id, action="typing")

    if "history" not in context.user_data:
        context.user_data["history"] = []

    history: list[dict] = context.user_data["history"]
    history.append({"role": "user", "content": user_text})

    if len(history) > MAX_HISTORY_MESSAGES:
        history = history[-MAX_HISTORY_MESSAGES:]
        context.user_data["history"] = history

    try:
        messages = list(history)
        max_tool_iterations = 8  # Evitar bucles infinitos

        for iteration in range(max_tool_iterations):
            prompt = _build_prompt(messages)
            logger.info("Llamando a claude -p (iteración %d)", iteration + 1)

            await context.bot.send_chat_action(
                chat_id=update.effective_chat.id, action="typing"
            )

            response_text = _call_claude(prompt)
            logger.info("Claude respondió: %s", response_text[:200])

            # ¿Es una llamada a herramienta?
            tool_call = _try_parse_tool_call(response_text)

            if tool_call:
                tool_name = tool_call["tool"]
                tool_args = tool_call.get("args", {})
                logger.info("Tool call: %s(%s)", tool_name, json.dumps(tool_args))

                if tool_name in TOOL_MAP:
                    tool_result = TOOL_MAP[tool_name](tool_args)
                else:
                    tool_result = f"Error: herramienta desconocida '{tool_name}'"

                # Añadir al contexto de la conversación para la siguiente iteración
                messages.append({"role": "assistant", "content": response_text})
                messages.append({
                    "role": "tool_result",
                    "content": f"[Resultado de {tool_name}]: {tool_result}",
                })

            else:
                # Respuesta final — enviar al usuario
                history.append({"role": "assistant", "content": response_text})
                context.user_data["history"] = history[-MAX_HISTORY_MESSAGES:]

                # Dividir si supera el límite de Telegram (4096 chars)
                if len(response_text) <= 4000:
                    await update.message.reply_text(response_text)
                else:
                    parts = [response_text[i:i + 4000] for i in range(0, len(response_text), 4000)]
                    for part in parts:
                        await update.message.reply_text(part)
                return

        # Si llega aquí, se agotaron las iteraciones
        await update.message.reply_text(
            "La operación requirió demasiados pasos. Intenta con una solicitud más simple."
        )

    except subprocess.TimeoutExpired:
        logger.error("claude -p tardó demasiado (timeout)")
        await update.message.reply_text(
            "La respuesta tardó demasiado. Intenta de nuevo con una solicitud más corta."
        )
    except RuntimeError as e:
        logger.error("Error en claude -p: %s", e)
        await update.message.reply_text(
            f"Error al invocar Claude: {e}\n"
            "¿Están las credenciales copiadas en EC2? Verifica con: `claude -p 'test'`"
        )
    except Exception as e:
        logger.exception("Error inesperado procesando mensaje")
        await update.message.reply_text(
            f"Error inesperado: {type(e).__name__}: {e}"
        )


# ==============================================================
# Entry point
# ==============================================================
def main() -> None:
    if not TELEGRAM_BOT_TOKEN:
        logger.error("TELEGRAM_BOT_TOKEN no configurado. Revisa el archivo .env")
        sys.exit(1)

    # Verificar que claude está disponible
    try:
        result = subprocess.run(
            [CLAUDE_BIN, "--version"],
            capture_output=True, text=True, timeout=10
        )
        logger.info("Claude Code disponible: %s", result.stdout.strip())
    except FileNotFoundError:
        logger.error(
            "Claude Code no encontrado en PATH. "
            "Instala con: npm install -g @anthropic-ai/claude-code"
        )
        sys.exit(1)

    logger.info("Iniciando bot de Telegram (backend: claude -p)")

    app = Application.builder().token(TELEGRAM_BOT_TOKEN).build()

    app.add_handler(CommandHandler("start", cmd_start))
    app.add_handler(CommandHandler("help", cmd_help))
    app.add_handler(CommandHandler("files", cmd_files))
    app.add_handler(CommandHandler("clear", cmd_clear))
    app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, handle_message))

    logger.info("Bot iniciado. Esperando mensajes...")
    app.run_polling(allowed_updates=Update.ALL_TYPES)


if __name__ == "__main__":
    main()
