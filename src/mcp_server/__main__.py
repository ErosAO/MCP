"""Entry point para: python -m mcp_server [transport]"""
import sys
from .server import run

transport = sys.argv[1] if len(sys.argv) > 1 else "sse"
run(transport)
