# Herramientas MCP

El servidor expone 6 herramientas via protocolo MCP. Todas operan dentro del directorio `FILES_DIR` y estan protegidas contra path traversal.

---

## `read_file`

Lee el contenido de un archivo de texto.

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `filename` | string | Si | Nombre o ruta relativa del archivo (ej: `notas.txt`, `docs/reporte.txt`) |

**Limites**
- Tamano maximo: `MAX_FILE_SIZE_MB` (default 10 MB)
- Solo archivos de texto (UTF-8). Binarios retornan error descriptivo.

**Ejemplo**
```
read_file("notas.txt")
# → "Contenido del archivo..."
```

---

## `write_file`

Crea o sobreescribe un archivo de texto. Crea subdirectorios intermedios automaticamente.

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `filename` | string | Si | Ruta relativa del archivo a crear/sobreescribir |
| `content` | string | Si | Contenido de texto a guardar |

**Ejemplo**
```
write_file("docs/resumen.txt", "Este es el resumen...")
# → "Archivo 'docs/resumen.txt' guardado correctamente (42 bytes, 42 caracteres)."
```

---

## `list_files`

Lista archivos y directorios con metadatos basicos.

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `directory` | string | No | Subdirectorio a listar. Vacio = directorio raiz |

**Salida**

```
Contenido de /  (3 elementos):
[DIR]  docs/  (2 elementos)
[FILE] notas.txt  (1,234 bytes)
[FILE] reporte.csv  (8,901 bytes)
```

---

## `delete_file`

Elimina un archivo o directorio (incluyendo su contenido si es directorio).

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `filename` | string | Si | Ruta relativa del archivo o directorio a eliminar |

**Ejemplo**
```
delete_file("borrador.txt")
# → "Archivo 'borrador.txt' eliminado correctamente."

delete_file("carpeta_vieja")
# → "Directorio 'carpeta_vieja' eliminado correctamente."
```

---

## `search_in_files`

Busca texto en todos los archivos de texto (busqueda insensible a mayusculas/minusculas). Muestra hasta 5 coincidencias por archivo.

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `query` | string | Si | Texto a buscar |
| `directory` | string | No | Directorio donde buscar. Vacio = busca en todos |

**Extensiones buscadas**
`.txt` `.md` `.csv` `.json` `.yaml` `.yml` `.log` `.ini` `.cfg` `.toml` `.html` `.xml` `.rst` `.org` y archivos sin extension.

**Salida**
```
Se encontro 'presupuesto' en 2 archivo(s) (de 5 revisados):

=== finanzas/q1.txt ===
  Linea 12: El presupuesto anual es de $50,000
  Linea 45: Revision de presupuesto pendiente

=== notas.txt ===
  Linea 3: presupuesto aprobado por direccion
```

---

## `get_file_info`

Obtiene metadatos de un archivo o directorio.

**Parametros**

| Parametro | Tipo | Requerido | Descripcion |
|-----------|------|-----------|-------------|
| `filename` | string | Si | Ruta relativa del archivo |

**Salida**
```
Nombre    : reporte.txt
Ruta      : docs/reporte.txt
Tipo      : Archivo
Tamano    : 4,096 bytes (4.0 KB)
Creado    : 2026-04-15 10:30:00
Modificado: 2026-04-19 08:15:22
Extension : .txt
```

---

## Seguridad: proteccion contra path traversal

Todas las herramientas pasan por `_safe_path()` en `tools.py`:

```python
def _safe_path(filename: str) -> Path:
    resolved = (FILES_DIR / filename.strip()).resolve()
    if not str(resolved).startswith(str(FILES_DIR.resolve())):
        raise ValueError("Acceso denegado: la ruta esta fuera del directorio permitido")
    return resolved
```

Intentar acceder a `../../etc/passwd` o rutas absolutas fuera de `FILES_DIR` retorna un error de acceso denegado.
