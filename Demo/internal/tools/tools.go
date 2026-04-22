package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/erosao/mcp/internal/config"
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".log": true, ".ini": true,
	".cfg": true, ".toml": true, ".html": true, ".xml": true,
	".rst": true, ".org": true, "": true,
}

func safePath(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	filesDir, err := filepath.Abs(config.FilesDir)
	if err != nil {
		return "", err
	}
	if filename == "" {
		return filesDir, nil
	}
	resolved, err := filepath.Abs(filepath.Join(filesDir, filename))
	if err != nil {
		return "", err
	}
	if resolved != filesDir && !strings.HasPrefix(resolved, filesDir+string(filepath.Separator)) {
		return "", fmt.Errorf("acceso denegado: la ruta está fuera del directorio permitido")
	}
	return resolved, nil
}

func ReadFile(filename string) string {
	path, err := safePath(filename)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Error: El archivo '%s' no existe.", filename)
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: '%s' es un directorio, no un archivo.", filename)
	}
	if sizeMB := float64(info.Size()) / (1024 * 1024); sizeMB > float64(config.MaxFileSizeMB) {
		return fmt.Sprintf("Error: Archivo demasiado grande (%.1f MB). Máximo: %d MB.", sizeMB, config.MaxFileSizeMB)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "Error al leer el archivo: " + err.Error()
	}
	return string(data)
}

func WriteFile(filename, content string) string {
	path, err := safePath(filename)
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "Error al crear directorios: " + err.Error()
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "Error al escribir el archivo: " + err.Error()
	}
	info, _ := os.Stat(path)
	return fmt.Sprintf("Archivo '%s' guardado correctamente (%d bytes, %d caracteres).",
		filename, info.Size(), len([]rune(content)))
}

func ListFiles(directory string) string {
	var path string
	var err error
	if directory == "" {
		path, err = filepath.Abs(config.FilesDir)
	} else {
		path, err = safePath(directory)
	}
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Error: El directorio '%s' no existe.", directory)
	}
	if !info.IsDir() {
		return fmt.Sprintf("Error: '%s' es un archivo, no un directorio.", directory)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "Error al listar directorio: " + err.Error()
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	filesDir, _ := filepath.Abs(config.FilesDir)
	var items []string
	for _, e := range entries {
		fullPath := filepath.Join(path, e.Name())
		rel, _ := filepath.Rel(filesDir, fullPath)
		if e.IsDir() {
			sub, _ := os.ReadDir(fullPath)
			items = append(items, fmt.Sprintf("[DIR]  %s/  (%d elementos)", rel, len(sub)))
		} else {
			fi, _ := e.Info()
			items = append(items, fmt.Sprintf("[FILE] %s  (%s bytes)", rel, formatInt(fi.Size())))
		}
	}
	if len(items) == 0 {
		return "El directorio está vacío."
	}
	label := "/"
	if directory != "" {
		label = directory
	}
	return fmt.Sprintf("Contenido de %s  (%d elementos):\n%s", label, len(items), strings.Join(items, "\n"))
}

func DeleteFile(filename string) string {
	path, err := safePath(filename)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Error: '%s' no existe.", filename)
	}
	if info.IsDir() {
		if err := os.RemoveAll(path); err != nil {
			return "Error al eliminar directorio: " + err.Error()
		}
		return fmt.Sprintf("Directorio '%s' eliminado correctamente.", filename)
	}
	if err := os.Remove(path); err != nil {
		return "Error al eliminar: " + err.Error()
	}
	return fmt.Sprintf("Archivo '%s' eliminado correctamente.", filename)
}

func SearchInFiles(query, directory string) string {
	if strings.TrimSpace(query) == "" {
		return "Error: La búsqueda no puede estar vacía."
	}
	var searchDir string
	var err error
	if directory == "" {
		searchDir, err = filepath.Abs(config.FilesDir)
	} else {
		searchDir, err = safePath(directory)
	}
	if err != nil {
		return "Error: " + err.Error()
	}
	if _, err := os.Stat(searchDir); err != nil {
		return "Error: El directorio de búsqueda no existe."
	}

	filesDir, _ := filepath.Abs(config.FilesDir)
	queryLower := strings.ToLower(query)
	var results []string
	filesSearched := 0

	filepath.WalkDir(searchDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		filesSearched++
		lines := strings.Split(string(data), "\n")
		var matches [][2]string
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), queryLower) {
				matches = append(matches, [2]string{fmt.Sprintf("%d", i+1), line})
			}
		}
		if len(matches) == 0 {
			return nil
		}
		rel, _ := filepath.Rel(filesDir, p)
		results = append(results, fmt.Sprintf("\n=== %s ===", rel))
		shown := matches
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, m := range shown {
			line := m[1]
			if len(line) > 200 {
				line = line[:200]
			}
			results = append(results, fmt.Sprintf("  Línea %s: %s", m[0], strings.TrimSpace(line)))
		}
		if len(matches) > 5 {
			results = append(results, fmt.Sprintf("  ... y %d coincidencia(s) más", len(matches)-5))
		}
		return nil
	})

	if len(results) == 0 {
		return fmt.Sprintf("No se encontró '%s' en %d archivo(s) revisado(s).", query, filesSearched)
	}
	return fmt.Sprintf("Se encontró '%s' en %d archivo(s) (de %d revisados):%s",
		query, len(results), filesSearched, strings.Join(results, ""))
}

func GetFileInfo(filename string) string {
	path, err := safePath(filename)
	if err != nil {
		return "Error: " + err.Error()
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Error: '%s' no existe.", filename)
	}
	filesDir, _ := filepath.Abs(config.FilesDir)
	rel, _ := filepath.Rel(filesDir, path)
	fileType := "Archivo"
	if info.IsDir() {
		fileType = "Directorio"
	}
	lines := []string{
		fmt.Sprintf("Nombre    : %s", info.Name()),
		fmt.Sprintf("Ruta      : %s", rel),
		fmt.Sprintf("Tipo      : %s", fileType),
		fmt.Sprintf("Tamaño    : %s bytes (%.1f KB)", formatInt(info.Size()), float64(info.Size())/1024),
		fmt.Sprintf("Modificado: %s", info.ModTime().Format("2006-01-02 15:04:05")),
	}
	if info.IsDir() {
		entries, _ := os.ReadDir(path)
		lines = append(lines, fmt.Sprintf("Elementos : %d", len(entries)))
	} else if ext := filepath.Ext(info.Name()); ext != "" {
		lines = append(lines, fmt.Sprintf("Extensión : %s", ext))
	}
	return strings.Join(lines, "\n")
}

func FormatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.2f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
