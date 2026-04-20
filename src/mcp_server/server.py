"""
FastMCP Server - Expone las herramientas de archivos via protocolo MCP.

Este servidor puede ser usado por clientes MCP como Claude Desktop.
El bot de Telegram importa las mismas herramientas directamente.

Uso:
    # SSE (recomendado para servidor persistente):
    python -m mcp_server sse

    # stdio (para clientes MCP locales):
    python -m mcp_server stdio
"""
import sys

from mcp.server.fastmcp import FastMCP

from . import tools as _tools
from .config import MCP_SERVER_HOST, MCP_SERVER_PORT

mcp = FastMCP(
    name="Claude MCP File Server",
    instructions=(
        "Servidor de archivos MCP. Permite leer, escribir, listar, "
        "buscar y gestionar archivos de texto en un directorio seguro."
    ),
)


@mcp.tool()
def read_file(filename: str) -> str:
    """
    Lee el contenido de un archivo de texto.

    Args:
        filename: Nombre o ruta relativa del archivo (ej: 'notas.txt' o 'docs/reporte.txt')
    """
    return _tools.read_file(filename)


@mcp.tool()
def write_file(filename: str, content: str) -> str:
    """
    Escribe contenido en un archivo de texto. Lo crea si no existe.

    Args:
        filename: Nombre o ruta relativa del archivo a crear/sobreescribir
        content:  Contenido de texto a guardar
    """
    return _tools.write_file(filename, content)


@mcp.tool()
def list_files(directory: str = "") -> str:
    """
    Lista archivos y directorios disponibles.

    Args:
        directory: Subdirectorio a listar. Dejar vacío para el directorio raíz.
    """
    return _tools.list_files(directory)


@mcp.tool()
def delete_file(filename: str) -> str:
    """
    Elimina un archivo o directorio.

    Args:
        filename: Nombre o ruta relativa del archivo/directorio a eliminar
    """
    return _tools.delete_file(filename)


@mcp.tool()
def search_in_files(query: str, directory: str = "") -> str:
    """
    Busca texto en todos los archivos (búsqueda insensible a mayúsculas).

    Args:
        query:     Texto a buscar
        directory: Directorio donde buscar. Dejar vacío para buscar en todos.
    """
    return _tools.search_in_files(query, directory)


@mcp.tool()
def get_file_info(filename: str) -> str:
    """
    Obtiene metadatos de un archivo: tamaño, fechas de creación y modificación.

    Args:
        filename: Nombre o ruta relativa del archivo
    """
    return _tools.get_file_info(filename)


def run(transport: str = "sse") -> None:
    """Arranca el servidor MCP con el transporte especificado."""
    if transport in ("sse", "streamable-http"):
        mcp.run(transport=transport, host=MCP_SERVER_HOST, port=MCP_SERVER_PORT)
    else:
        mcp.run()  # stdio


if __name__ == "__main__":
    transport_arg = sys.argv[1] if len(sys.argv) > 1 else "sse"
    run(transport_arg)
