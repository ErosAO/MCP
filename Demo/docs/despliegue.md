# Despliegue en AWS

El proyecto incluye un stack de CloudFormation que despliega una instancia EC2 ARM con VPC, subnet publica y todo lo necesario para produccion.

## Arquitectura desplegada

```
AWS (us-east-2 por default)
  └─ VPC (10.0.0.0/16)
      └─ Subnet publica
          └─ Security Group (SSH + trafico de salida)
              └─ EC2 t4g.micro ARM Graviton2 — Amazon Linux 2023
                  ├─ /opt/mcp/app/     codigo fuente
                  ├─ /opt/mcp/files/   archivos gestionados
                  ├─ /opt/mcp/logs/    logs
                  └─ systemd:
                      ├─ mcp-server.service   → SSE en 127.0.0.1:8000
                      └─ telegram-bot.service → polling Telegram
```

## Prerrequisitos

- AWS CLI configurado (`aws configure`)
- Key Pair EC2 creado en la region de destino
- `.env` con `TELEGRAM_BOT_TOKEN` configurado
- Sesion de Claude Code autenticada en tu maquina local (`~/.claude/.claude.json`)

## Deploy completo (infra + app)

```bash
# Opcion 1: con flags
./infra/scripts/deploy.sh \
  -k mi-key-pair \
  -f ~/.ssh/mi-key.pem \
  -r us-east-2 \
  -t t4g.micro

# Opcion 2: con variables de entorno
export KEY_PAIR_NAME=mi-key-pair
export KEY_FILE=~/.ssh/mi-key.pem
export AWS_REGION=us-east-2
./infra/scripts/deploy.sh
```

## Opciones del script de deploy

| Flag | Env var | Default | Descripcion |
|------|---------|---------|-------------|
| `-k, --key-pair` | `KEY_PAIR_NAME` | — | Nombre del Key Pair EC2 (requerido para infra) |
| `-f, --key-file` | `KEY_FILE` | — | Ruta al archivo `.pem` para SSH (requerido para app) |
| `-s, --stack` | `STACK_NAME` | `mcp-server` | Nombre del stack CloudFormation |
| `-r, --region` | `AWS_REGION` | `us-east-1` | Region AWS |
| `-t, --type` | `INSTANCE_TYPE` | `t4g.micro` | Tipo de instancia EC2 |
| `--cidr` | `SSH_CIDR` | `0.0.0.0/0` | CIDR para acceso SSH (restringe a tu IP en prod) |
| `--infra-only` | — | false | Solo desplegar CloudFormation, sin codigo |
| `--app-only` | — | false | Solo desplegar codigo, infraestructura ya existe |
| `-y, --yes` | — | false | No pedir confirmacion |

## Tipos de instancia disponibles

| Tipo | RAM | Costo aprox/hr | Recomendado para |
|------|-----|----------------|-----------------|
| `t4g.nano` | 0.5 GB | ~$0.0042 | Pruebas/desarrollo |
| `t4g.micro` | 1 GB | ~$0.0084 | **Produccion (recomendado)** |
| `t4g.small` | 2 GB | ~$0.0168 | Alta carga |

## Paso manual: autenticar Claude Code en EC2

El deploy automatico copia las credenciales OAuth de `~/.claude/` si existen. Si no:

```bash
# 1. Conectarte al EC2
ssh -i ~/.ssh/mi-key.pem ec2-user@<IP-DE-INSTANCIA>

# 2. Ejecutar el login interactivo de Claude
claude
# Se abre una URL en terminal - abrela en tu navegador y acepta

# 3. Verificar que funciona
claude -p "Responde solo: OK"

# 4. Levantar los servicios
sudo systemctl start mcp-server telegram-bot
sudo systemctl status mcp-server telegram-bot
```

## Ver logs en produccion

```bash
# Logs del bot de Telegram
sudo journalctl -u telegram-bot -f

# Logs del servidor MCP
sudo journalctl -u mcp-server -f

# Logs de archivo
tail -f /opt/mcp/logs/telegram-bot.log
```

## Conectarse via SSH tunnel

```bash
./infra/scripts/ssh-connect.sh
```

El script abre un tunnel SSH al EC2 y reenvía el puerto 8000 (MCP server) localmente.

## Destruir la infraestructura

```bash
./infra/scripts/teardown.sh
```

Elimina el stack de CloudFormation y todos los recursos asociados (VPC, EC2, Security Groups).

> **Atencion:** Los archivos en `/opt/mcp/files/` se pierden al destruir la instancia. Haz backup antes si es necesario.

## Crear un Key Pair nuevo (si no tienes uno)

```bash
aws ec2 create-key-pair \
  --key-name mi-key \
  --region us-east-2 \
  --query 'KeyMaterial' \
  --output text > ~/.ssh/mi-key.pem

chmod 400 ~/.ssh/mi-key.pem
```
