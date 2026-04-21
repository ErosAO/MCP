# Arquitectura

## Diagrama

```
                         INTERNET
                            │
               ┌────────────┼────────────┐
               │                         │
        ┌──────▼──────┐         ┌────────▼────────┐
        │  Telegram   │         │   GitHub API    │
        │   Bot API   │         │  (Aeromexico)   │
        └──────┬──────┘         └────────┬────────┘
               │                         │
     ─ ─ ─ ─ AWS VPC 10.0.0.0/16 ─ ─ ─ ─│─ ─ ─ ─ ─
     │                                   │         │
     │   ┌─────────────────┐   ┌─────────▼───────┐ │
     │   │   Bot EC2       │   │  MCP Server EC2 │ │
     │   │  (t4g.micro)    │   │  (t4g.micro)    │ │
     │   │                 │   │                 │ │
     │   │  telegram-bot   ├──►│  :8081 API REST │ │
     │   │  (Go binary)    │   │  :8080 MCP SSE  │ │
     │   │                 │   │                 │ │
     │   │  Elastic IP ←───┼──►│  IP privada     │ │
     │   │  (pública fija) │   │  10.0.1.x       │ │
     │   └─────────────────┘   └─────────────────┘ │
     │                                             │
     ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─
```

## Componentes

### EC2 Bot (Telegram Bot)
- **Binario**: `telegram-bot` (Go)
- **Función**: Recibir comandos de Telegram, mostrar teclado de deploys, llamar al MCP server
- **Comunicación saliente**: Telegram API (internet), MCP Server (VPC privada)
- **IP**: Elastic IP fija (pública)
- **Puerto**: Sin puertos inbound expuestos (solo SSH para admin)

### EC2 MCP Server
- **Binario**: `mcp-server` (Go)
- **Puertos**:
  - `:8080` — SSE endpoint MCP (para clientes Claude Code)
  - `:8081` — API REST interna (solo accesible desde Bot via SG)
- **Función**:
  1. Recibir solicitudes de deploy del Bot o Claude
  2. Ejecutar `github-deploy.sh` con los tokens de GitHub
  3. Enviar notificaciones Telegram en cada etapa
  4. Monitorear status del PR en GitHub (polling cada 30s)
- **IP**: Privada (solo accesible desde el Bot dentro de la VPC)
- **Outbound**: GitHub API, Telegram API

## Seguridad

| Componente | Inbound | Outbound |
|------------|---------|----------|
| Bot SG | SSH (admin) | Todo |
| MCP SG | SSH (admin) + TCP 8080-8081 (solo desde Bot SG) | Todo |

Los tokens de GitHub **nunca** viajan por internet — están en el `.env` local del MCP server y se pasan al script como variables de entorno.

## Comunicación Bot → MCP

El Bot llama a `POST http://<mcp-private-ip>:8081/internal/deploy` con JSON:

```json
{
  "action": "deploy_dev_facturacion",
  "requested_by": "username",
  "chat_id": 123456789
}
```

Respuesta inmediata (el deploy corre en background):

```json
{
  "success": true,
  "message": "🚀 Deploy Dev Facturación iniciado. Recibirás notificación al terminar."
}
```

## Flujo de notificaciones

```
1. Bot llama a MCP              → MCP responde: "Deploy iniciado"
2. MCP ejecuta script           → Notifica: "⏳ En progreso..."
3. Script crea PR en GitHub     → Notifica: "✅ PR creado: https://..."
4. Monitor polling GitHub/30s   → Notifica: "✅ PR mergeado" o "❌ PR cerrado sin merge"
```
