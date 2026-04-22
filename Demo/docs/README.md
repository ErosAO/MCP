# Claude MCP File Server — Documentacion

Sistema de gestion de archivos potenciado por Claude AI, accesible via protocolo MCP y Telegram.

## Indice

| Documento | Descripcion |
|-----------|-------------|
| [Arquitectura](arquitectura.md) | Vision general del sistema y flujo de datos |
| [Configuracion](configuracion.md) | Variables de entorno y ajustes del servidor |
| [Herramientas MCP](herramientas-mcp.md) | Referencia de las 6 herramientas expuestas |
| [Bot de Telegram](telegram-bot.md) | Guia del bot: comandos, autorizacion y flujo agentico |
| [Desarrollo Local](desarrollo.md) | Como ejecutar el proyecto en tu maquina |
| [Despliegue en AWS](despliegue.md) | Deploy completo en EC2 via CloudFormation |
| [Plantilla MCP](plantilla-mcp.md) | Referencia personal: patron para construir nuevos proyectos MCP |

## Resumen rapido

```
Usuario
  │
  ├─► Telegram Bot ──► claude -p (CLI) ──► Tools (archivos)
  │
  └─► Cliente MCP ──────────────────────► MCP Server (FastMCP, SSE/stdio)
                                                │
                                          FILES_DIR (sandboxed)
```

**Stack:** Python 3.11 · FastMCP · python-telegram-bot · AWS EC2 · systemd
