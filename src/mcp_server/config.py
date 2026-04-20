"""Configuración central del MCP Server y Telegram Bot."""
import os
from pathlib import Path

from dotenv import load_dotenv

# Buscar .env en múltiples ubicaciones (producción → desarrollo)
_env_candidates = [
    Path("/opt/mcp/app/.env"),                                  # Producción EC2
    Path(__file__).parent.parent.parent / ".env",               # Raíz del repo
    Path.cwd() / ".env",                                        # Directorio actual
]
for _env_path in _env_candidates:
    if _env_path.exists():
        load_dotenv(_env_path)
        break

# --- Directorios ---
_default_files_dir = (
    "/opt/mcp/files"
    if Path("/opt/mcp").exists()
    else str(Path.cwd() / "files")
)
FILES_DIR = Path(os.getenv("FILES_DIR", _default_files_dir))
FILES_DIR.mkdir(parents=True, exist_ok=True)

_default_logs_dir = (
    "/opt/mcp/logs"
    if Path("/opt/mcp").exists()
    else str(Path.cwd() / "logs")
)
LOGS_DIR = Path(os.getenv("LOGS_DIR", _default_logs_dir))
LOGS_DIR.mkdir(parents=True, exist_ok=True)

# --- API Keys ---
# ANTHROPIC_API_KEY: solo necesario si usas el SDK de Anthropic directamente.
# Si usas Claude Code CLI (claude -p), NO es necesario.
ANTHROPIC_API_KEY: str = os.getenv("ANTHROPIC_API_KEY", "")

TELEGRAM_BOT_TOKEN: str = os.getenv("TELEGRAM_BOT_TOKEN", "")

# --- Modelo Claude ---
CLAUDE_MODEL: str = os.getenv("CLAUDE_MODEL", "claude-sonnet-4-6")

# --- MCP Server ---
MCP_SERVER_HOST: str = os.getenv("MCP_SERVER_HOST", "127.0.0.1")
MCP_SERVER_PORT: int = int(os.getenv("MCP_SERVER_PORT", "8000"))

# --- Seguridad del bot ---
# Lista de user IDs o usernames de Telegram autorizados (separados por coma)
# Si está vacío, cualquier usuario puede usar el bot
_raw_users = os.getenv("ALLOWED_TELEGRAM_USERS", "")
ALLOWED_TELEGRAM_USERS: list[str] = [u.strip() for u in _raw_users.split(",") if u.strip()]

# --- Límites ---
MAX_FILE_SIZE_MB: int = int(os.getenv("MAX_FILE_SIZE_MB", "10"))
MAX_HISTORY_MESSAGES: int = int(os.getenv("MAX_HISTORY_MESSAGES", "20"))
