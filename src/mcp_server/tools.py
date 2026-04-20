"""
Implementaciones puras de las herramientas de archivos del MCP Server.

Todas las funciones son independientes y pueden ser importadas
tanto por el servidor MCP como por el bot de Telegram.
"""
import datetime
import shutil
from pathlib import Path

from .config import FILES_DIR, MAX_FILE_SIZE_MB

# Extensiones de texto que se pueden buscar/leer
TEXT_EXTENSIONS = {
    ".txt", ".md", ".csv", ".json", ".yaml", ".yml",
    ".log", ".ini", ".cfg", ".toml", ".html", ".xml",
    ".rst", ".org", "",
}


def _safe_path(filename: str) -> Path:
    """
    Resuelve la ruta y verifica que esté dentro de FILES_DIR.
    Previene path traversal (ej: '../../etc/passwd').
    """
    if not filename or not filename.strip():
        return FILES_DIR
    resolved = (FILES_DIR / filename.strip()).resolve()
    if not str(resolved).startswith(str(FILES_DIR.resolve())):
        raise ValueError("Acceso denegado: la ruta está fuera del directorio permitido")
    return resolved


# ==============================================================
# Herramienta: read_file
# ==============================================================
def read_file(filename: str) -> str:
    """
    Lee el contenido de un archivo de texto.

    Args:
        filename: Nombre o ruta relativa del archivo a leer.

    Returns:
        Contenido del archivo como texto, o mensaje de error.
    """
    try:
        path = _safe_path(filename)
    except ValueError as e:
        return f"Error: {e}"

    if not path.exists():
        return f"Error: El archivo '{filename}' no existe."
    if not path.is_file():
        return f"Error: '{filename}' es un directorio, no un archivo."

    size_mb = path.stat().st_size / (1024 * 1024)
    if size_mb > MAX_FILE_SIZE_MB:
        return (
            f"Error: Archivo demasiado grande ({size_mb:.1f} MB). "
            f"Máximo permitido: {MAX_FILE_SIZE_MB} MB."
        )

    try:
        return path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        return f"Error: '{filename}' no parece ser un archivo de texto (posible binario)."
    except Exception as e:
        return f"Error al leer el archivo: {e}"


# ==============================================================
# Herramienta: write_file
# ==============================================================
def write_file(filename: str, content: str) -> str:
    """
    Escribe contenido en un archivo de texto.
    Crea el archivo si no existe; lo sobreescribe si ya existe.

    Args:
        filename: Nombre o ruta relativa del archivo a escribir.
        content:  Contenido a escribir.

    Returns:
        Mensaje de éxito o error.
    """
    try:
        path = _safe_path(filename)
    except ValueError as e:
        return f"Error: {e}"

    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        size = path.stat().st_size
        return f"Archivo '{filename}' guardado correctamente ({size} bytes, {len(content)} caracteres)."
    except Exception as e:
        return f"Error al escribir el archivo: {e}"


# ==============================================================
# Herramienta: list_files
# ==============================================================
def list_files(directory: str = "") -> str:
    """
    Lista archivos y directorios.

    Args:
        directory: Subdirectorio a listar (vacío = directorio raíz).

    Returns:
        Lista formateada de archivos y directorios.
    """
    try:
        path = _safe_path(directory) if directory else FILES_DIR
    except ValueError as e:
        return f"Error: {e}"

    if not path.exists():
        return f"Error: El directorio '{directory}' no existe."
    if not path.is_dir():
        return f"Error: '{directory}' es un archivo, no un directorio."

    items = []
    for item in sorted(path.iterdir()):
        try:
            rel = item.relative_to(FILES_DIR)
            if item.is_dir():
                count = sum(1 for _ in item.iterdir())
                items.append(f"[DIR]  {rel}/  ({count} elementos)")
            else:
                size = item.stat().st_size
                items.append(f"[FILE] {rel}  ({size:,} bytes)")
        except Exception:
            continue

    if not items:
        return "El directorio está vacío."

    header = f"Contenido de {'/' if not directory else directory}  ({len(items)} elementos):\n"
    return header + "\n".join(items)


# ==============================================================
# Herramienta: delete_file
# ==============================================================
def delete_file(filename: str) -> str:
    """
    Elimina un archivo o directorio.

    Args:
        filename: Nombre o ruta relativa del archivo/directorio a eliminar.

    Returns:
        Mensaje de éxito o error.
    """
    try:
        path = _safe_path(filename)
    except ValueError as e:
        return f"Error: {e}"

    if not path.exists():
        return f"Error: '{filename}' no existe."

    try:
        if path.is_dir():
            shutil.rmtree(path)
            return f"Directorio '{filename}' eliminado correctamente."
        else:
            path.unlink()
            return f"Archivo '{filename}' eliminado correctamente."
    except Exception as e:
        return f"Error al eliminar: {e}"


# ==============================================================
# Herramienta: search_in_files
# ==============================================================
def search_in_files(query: str, directory: str = "") -> str:
    """
    Busca texto en todos los archivos de texto (insensible a mayúsculas).

    Args:
        query:     Texto a buscar.
        directory: Directorio donde buscar (vacío = busca en todos).

    Returns:
        Resultados de la búsqueda con nombre de archivo y líneas coincidentes.
    """
    if not query or not query.strip():
        return "Error: La búsqueda no puede estar vacía."

    try:
        search_dir = _safe_path(directory) if directory else FILES_DIR
    except ValueError as e:
        return f"Error: {e}"

    if not search_dir.exists():
        return f"Error: El directorio de búsqueda no existe."

    results = []
    files_searched = 0
    query_lower = query.lower()

    for path in sorted(search_dir.rglob("*")):
        if not path.is_file():
            continue
        if path.suffix.lower() not in TEXT_EXTENSIONS:
            continue

        try:
            content = path.read_text(encoding="utf-8", errors="ignore")
            files_searched += 1
            lines = content.splitlines()
            matches = [
                (i + 1, line)
                for i, line in enumerate(lines)
                if query_lower in line.lower()
            ]

            if matches:
                rel_path = path.relative_to(FILES_DIR)
                results.append(f"\n=== {rel_path} ===")
                shown = matches[:5]
                for line_num, line in shown:
                    results.append(f"  Línea {line_num}: {line.strip()[:200]}")
                if len(matches) > 5:
                    results.append(f"  ... y {len(matches) - 5} coincidencia(s) más")
        except Exception:
            continue

    if not results:
        return f"No se encontró '{query}' en {files_searched} archivo(s) revisado(s)."

    header = f"Se encontró '{query}' en {len(results)} archivo(s) (de {files_searched} revisados):"
    return header + "".join(results)


# ==============================================================
# Herramienta: get_file_info
# ==============================================================
def get_file_info(filename: str) -> str:
    """
    Obtiene metadatos de un archivo (tamaño, fechas, tipo).

    Args:
        filename: Nombre o ruta relativa del archivo.

    Returns:
        Información del archivo formateada.
    """
    try:
        path = _safe_path(filename)
    except ValueError as e:
        return f"Error: {e}"

    if not path.exists():
        return f"Error: '{filename}' no existe."

    try:
        stat = path.stat()
        modified = datetime.datetime.fromtimestamp(stat.st_mtime).strftime("%Y-%m-%d %H:%M:%S")
        created = datetime.datetime.fromtimestamp(stat.st_ctime).strftime("%Y-%m-%d %H:%M:%S")
        size_kb = stat.st_size / 1024

        lines = [
            f"Nombre    : {path.name}",
            f"Ruta      : {path.relative_to(FILES_DIR)}",
            f"Tipo      : {'Directorio' if path.is_dir() else 'Archivo'}",
            f"Tamaño    : {stat.st_size:,} bytes ({size_kb:.1f} KB)",
            f"Creado    : {created}",
            f"Modificado: {modified}",
        ]
        if path.is_dir():
            count = sum(1 for _ in path.iterdir())
            lines.append(f"Elementos : {count}")
        elif path.suffix:
            lines.append(f"Extensión : {path.suffix}")

        return "\n".join(lines)
    except Exception as e:
        return f"Error al obtener información: {e}"
