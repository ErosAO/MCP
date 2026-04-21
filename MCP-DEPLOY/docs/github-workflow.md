# Flujo GitHub

## Descripción general

```
Telegram Bot → MCP Server → github-deploy.sh → PR en Aeromexico → GitHub Actions → Deploy real
                                  ↑                                     ↓
                          Tokens GitHub                        Notificación Telegram
```

## Cuentas GitHub utilizadas

| Cuenta | Propósito | Variables de entorno |
|--------|-----------|---------------------|
| **Miatech** | Repositorio fuente (código a deployar) | `GITHUB_MIATECH_TOKEN`, `GITHUB_MIATECH_ORG` |
| **Aeromexico** | Repositorio destino (donde corre el deploy) | `GITHUB_AEROMEXICO_TOKEN`, `GITHUB_AEROMEXICO_ORG` |

## Flujo detallado del script `github-deploy.sh`

```bash
# Argumentos: $1=environment  $2=service
# Variables de entorno: tokens, orgs, repos

1. Clonar repo fuente (Miatech)     → usa GITHUB_MIATECH_TOKEN
2. Clonar repo destino (Aeromexico) → usa GITHUB_AEROMEXICO_TOKEN
3. Crear rama: deploy/<env>/<svc>/<timestamp>
4. Copiar archivos fuente → destino
5. Commit de los cambios
6. Push de la rama al repo de Aeromexico
7. Crear Pull Request en Aeromexico
8. Imprimir: "PR_URL: https://github.com/..."
```

## Salida requerida del script

El MCP server extrae la URL del PR de la salida del script. **El script debe imprimir**:

```
PR_URL: https://github.com/aeromexico/facturacion/pull/42
```

Sin esa línea, el monitoreo de PR no funcionará (pero el deploy sí se habrá ejecutado).

## Tokens GitHub — Permisos requeridos

### Token Miatech (`GITHUB_MIATECH_TOKEN`)

| Scope | Motivo |
|-------|--------|
| `repo` | Clonar repositorios privados |
| `read:org` | Listar repos de la organización |

**Crear en:** GitHub → Settings → Developer settings → Personal access tokens

### Token Aeromexico (`GITHUB_AEROMEXICO_TOKEN`)

| Scope | Motivo |
|-------|--------|
| `repo` | Clonar + push a repos privados |
| `workflow` | Disparar/ver GitHub Actions |
| `read:org` | Verificar que el repo existe |

> Los tokens son **secrets** — nunca los expongas en logs, código fuente ni mensajes de Telegram. El MCP server los pasa al script solo como variables de entorno, sin logearlos.

## Monitoreo de PRs

Después de crear el PR, el MCP server inicia un goroutine de monitoreo:

```
Cada 30 segundos → GET https://api.github.com/repos/<org>/<repo>/pulls/<number>

Si state == "closed" && merged == true  → ✅ Notificar éxito
Si state == "closed" && merged == false → ❌ Notificar fallo
Si pasan 2 horas sin resolución         → ⏰ Notificar timeout
```

El monitoreo usa el `GITHUB_AEROMEXICO_TOKEN` para consultar la API.

## Adaptando el script a tu estructura

El archivo `scripts/github-deploy.sh` es una plantilla. Ajusta estas secciones según tu repositorio real:

```bash
# Línea ~80: Rama fuente del repo Miatech
gh repo clone "$SOURCE_REPO" ... --branch=main

# Línea ~100: Rama destino del repo Aeromexico
TARGET_BRANCH="${ENVIRONMENT}"  # Puede ser "main", "develop", etc.

# Líneas ~93-100: Qué archivos copiar
rsync -av --exclude='.git' "$WORK_DIR/source/" "$WORK_DIR/dest/"
# Si solo quieres copiar una carpeta específica:
# rsync -av "$WORK_DIR/source/src/" "$WORK_DIR/dest/src/"
```

## Ejemplo de GitHub Actions en Aeromexico

El PR disparará automáticamente el workflow de GitHub Actions si tienes algo como:

```yaml
# .github/workflows/deploy.yml en repo de Aeromexico
name: Deploy on PR Merge

on:
  pull_request:
    types: [closed]
    branches:
      - dev
      - qa
      - prod

jobs:
  deploy:
    if: github.event.pull_request.merged == true
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Deploy
        run: |
          echo "Deployando a ${{ github.base_ref }}..."
          # Tu script de deploy aquí
```

## Troubleshooting

### El script falla al clonar

```bash
# Verificar token Miatech
export GH_TOKEN=ghp_xxxxx
gh repo view miatech/facturacion

# Verificar token Aeromexico
export GH_TOKEN=ghp_yyyyy
gh repo view aeromexico/facturacion
```

### No se crea el PR

```bash
# Verificar que la rama destino existe en el repo de Aeromexico
gh repo view aeromexico/facturacion --json defaultBranchRef
```

### El monitoreo no detecta el merge

```bash
# Verificar acceso a la API de GitHub
curl -H "Authorization: Bearer $GITHUB_AEROMEXICO_TOKEN" \
     https://api.github.com/repos/aeromexico/facturacion/pulls/1
```
