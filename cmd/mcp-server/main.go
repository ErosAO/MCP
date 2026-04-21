package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/erosao/mcp/internal/config"
	"github.com/erosao/mcp/internal/tools"
)

// strArg extrae un argumento string usando GetArguments() (API oficial de mcp-go v0.48+).
func strArg(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if args == nil {
		return ""
	}
	v, _ := args[key].(string)
	return v
}

func main() {
	transport := "sse"
	if len(os.Args) > 1 {
		transport = os.Args[1]
	}

	logFile, err := os.OpenFile(
		filepath.Join(config.LogsDir, "mcp-server.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644,
	)
	if err != nil {
		slog.Error("cannot open log file", "err", err)
		os.Exit(1)
	}
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logFile), nil)))

	s := server.NewMCPServer("Claude MCP File Server", "1.0.0")

	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Lee el contenido de un archivo de texto"),
			mcp.WithString("filename", mcp.Required(), mcp.Description("Nombre o ruta relativa del archivo")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.ReadFile(strArg(req, "filename"))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("Escribe contenido en un archivo de texto"),
			mcp.WithString("filename", mcp.Required(), mcp.Description("Nombre o ruta relativa del archivo")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Contenido a escribir")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.WriteFile(strArg(req, "filename"), strArg(req, "content"))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("list_files",
			mcp.WithDescription("Lista archivos y directorios"),
			mcp.WithString("directory", mcp.Description("Subdirectorio a listar (vacío = raíz)")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.ListFiles(strArg(req, "directory"))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("delete_file",
			mcp.WithDescription("Elimina un archivo o directorio"),
			mcp.WithString("filename", mcp.Required(), mcp.Description("Nombre o ruta relativa del archivo")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.DeleteFile(strArg(req, "filename"))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("search_in_files",
			mcp.WithDescription("Busca texto en archivos"),
			mcp.WithString("query", mcp.Required(), mcp.Description("Texto a buscar")),
			mcp.WithString("directory", mcp.Description("Directorio donde buscar")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.SearchInFiles(strArg(req, "query"), strArg(req, "directory"))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_file_info",
			mcp.WithDescription("Obtiene metadatos de un archivo"),
			mcp.WithString("filename", mcp.Required(), mcp.Description("Nombre o ruta relativa del archivo")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(tools.GetFileInfo(strArg(req, "filename"))), nil
		},
	)

	addr := fmt.Sprintf("%s:%d", config.MCPServerHost, config.MCPServerPort)

	switch strings.ToLower(transport) {
	case "sse", "streamable-http":
		slog.Info("Starting MCP SSE server", "addr", addr)
		httpServer := server.NewSSEServer(s, server.WithBaseURL(fmt.Sprintf("http://%s", addr)))
		if err := httpServer.Start(addr); err != nil {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	default:
		slog.Info("Starting MCP stdio server")
		if err := server.ServeStdio(s); err != nil {
			slog.Error("stdio server failed", "err", err)
			os.Exit(1)
		}
	}
}
