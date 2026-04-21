# MCP Deploy

Bot de Telegram + MCP Server para gestionar deploys de **Facturación** y **RAM** en Dev, QA y Producción via GitHub Actions.

## Índice

- [Arquitectura](arquitectura.md)
- [Configuración](configuracion.md)
- [Despliegue en AWS](despliegue.md)
- [Flujo GitHub](github-workflow.md)

## Acciones disponibles

| Acción | Descripción |
|--------|-------------|
| `deploy_dev_facturacion` | Deploy a Dev del servicio Facturación |
| `deploy_qa_facturacion` | Deploy a QA del servicio Facturación |
| `deploy_prod_facturacion` | Deploy a Prod del servicio Facturación |
| `deploy_dev_ram` | Deploy a Dev del servicio RAM |
| `deploy_qa_ram` | Deploy a QA del servicio RAM |
| `deploy_prod_ram` | Deploy a Prod del servicio RAM |

## Flujo de usuario

```
Usuario → Telegram Bot → MCP Server → github-deploy.sh
                                         ↓
                               GitHub Actions (Aeromexico)
                                         ↓
                               Notificación Telegram (PR mergeado / fallido)
```

## Estructura del proyecto

```
MCP-DEPLOY/
├── cmd/
│   ├── bot/main.go           Bot Telegram (EC2-1)
│   └── mcp-server/main.go   MCP Server + API interna (EC2-2)
├── internal/
│   ├── config/config.go     Variables de entorno
│   ├── deploy/deploy.go     Ejecución del script + monitor GitHub
│   └── notify/notify.go     Notificaciones Telegram desde MCP
├── scripts/
│   └── github-deploy.sh     Script de deploy (clonar + PR)
├── infra/
│   ├── cloudformation/stack.yml   Infraestructura AWS (2 EC2)
│   └── scripts/
│       ├── deploy.sh              Deploy completo
│       ├── teardown.sh            Eliminación
│       └── ssh-connect.sh         Conexión SSH
├── systemd/                 Servicios systemd
└── docs/                    Esta documentación
```
