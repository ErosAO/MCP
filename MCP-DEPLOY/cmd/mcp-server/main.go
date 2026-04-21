package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/erosao/mcp-deploy/internal/config"
	"github.com/erosao/mcp-deploy/internal/deploy"
	"github.com/erosao/mcp-deploy/internal/notify"
)

func strArg(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if args == nil {
		return ""
	}
	v, _ := args[key].(string)
	return v
}

// runDeploy ejecuta el deploy de forma sincrónica y envía notificaciones Telegram.
func runDeploy(dreq deploy.Request, requestedBy string, chatID int64) {
	slog.Info("deploy started",
		"flow", dreq.Flow, "scope", dreq.Scope, "user", dreq.User, "by", requestedBy)

	chatSet := map[int64]struct{}{}
	if chatID != 0 {
		chatSet[chatID] = struct{}{}
	}
	for _, id := range config.NotificationChatIDs {
		chatSet[id] = struct{}{}
	}
	broadcast := func(text string) {
		for id := range chatSet {
			notify.Send(config.TelegramBotToken, id, text)
		}
	}

	broadcast(fmt.Sprintf(
		"⏳ <b>Deploy %s</b> en progreso...\nScope: <b>%s</b>  |  Usuario: <b>%s</b>\nSolicitado por: @%s",
		dreq.Flow.Label(), dreq.Scope, dreq.User, requestedBy,
	))

	result := deploy.Execute(dreq, config.DeployScriptPath)

	slog.Info("deploy finished", "exit_code", result.ExitCode, "failures", len(result.Summary.Failures))
	broadcast(result.FormatTelegram(dreq))
}

// ─── Internal REST API ────────────────────────────────────────────────────────

type apiDeployReq struct {
	Flow        string `json:"flow"`
	Scope       string `json:"scope"`
	User        string `json:"user"`
	RequestedBy string `json:"requested_by"`
	ChatID      int64  `json:"chat_id"`
}

type apiDeployResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var validFlows = map[string]bool{
	"miatech-to-dev": true,
	"miatech-to-qa":  true,
	"dev-to-qa":      true,
	"qa-to-prod":     true,
}

var validScopes = map[string]bool{
	"individual": true,
	"global":     true,
	"both":       true,
}

func startInternalAPI() {
	mux := http.NewServeMux()

	mux.HandleFunc("/internal/deploy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(apiDeployResp{false, "method not allowed"})
			return
		}

		var req apiDeployReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(apiDeployResp{false, "request inválida: " + err.Error()})
			return
		}

		if !validFlows[req.Flow] {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(apiDeployResp{false,
				fmt.Sprintf("flow inválido: %q (opciones: miatech-to-dev | miatech-to-qa | dev-to-qa | qa-to-prod)", req.Flow)})
			return
		}
		if !validScopes[req.Scope] {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(apiDeployResp{false,
				fmt.Sprintf("scope inválido: %q (opciones: individual | global | both)", req.Scope)})
			return
		}

		user := deploy.User(req.User)
		if user == "" {
			user = deploy.UserDeployer
		}
		dreq := deploy.Request{
			Flow:  deploy.Flow(req.Flow),
			Scope: deploy.Scope(req.Scope),
			User:  user,
		}
		go runDeploy(dreq, req.RequestedBy, req.ChatID)

		json.NewEncoder(w).Encode(apiDeployResp{
			Success: true,
			Message: fmt.Sprintf("🚀 Deploy <b>%s</b> iniciado. Recibirás notificación al terminar.", dreq.Flow.Label()),
		})
	})

	mux.HandleFunc("/internal/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "mcp-deploy"})
	})

	addr := fmt.Sprintf("0.0.0.0:%d", config.MCPAPIPort)
	slog.Info("Starting internal API", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("internal API server failed", "err", err)
		os.Exit(1)
	}
}

func main() {
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

	// API interna para el bot (puerto 8081)
	go startInternalAPI()

	// Servidor MCP SSE para clientes Claude (puerto 8080)
	s := server.NewMCPServer("MCP Deploy Server", "1.0.0")

	s.AddTool(
		mcp.NewTool("deploy",
			mcp.WithDescription("Ejecuta deployer.sh para sincronizar y desplegar el servicio Facturación entre ambientes."),
			mcp.WithString("flow",
				mcp.Required(),
				mcp.Description("Flujo de deploy: miatech-to-dev | miatech-to-qa | dev-to-qa | qa-to-prod"),
			),
			mcp.WithString("scope",
				mcp.Required(),
				mcp.Description("Alcance del deploy: individual | global | both"),
			),
			mcp.WithString("user",
				mcp.Description("Usuario que ejecuta el script: franramvel | ErosAO | Haztel05 | deployer (default)"),
			),
			mcp.WithString("requested_by",
				mcp.Description("Username de Telegram de quien solicita el deploy"),
			),
			mcp.WithString("chat_id",
				mcp.Description("Chat ID de Telegram para notificaciones (string numérico)"),
			),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			flow := deploy.Flow(strArg(req, "flow"))
			scope := deploy.Scope(strArg(req, "scope"))
			user := deploy.User(strArg(req, "user"))
			if user == "" {
				user = deploy.UserDeployer
			}
			requestedBy := strArg(req, "requested_by")

			var chatID int64
			if s := strArg(req, "chat_id"); s != "" {
				chatID, _ = strconv.ParseInt(s, 10, 64)
			}

			dreq := deploy.Request{Flow: flow, Scope: scope, User: user}
			go runDeploy(dreq, requestedBy, chatID)

			return mcp.NewToolResultText(fmt.Sprintf(
				"🚀 Deploy %s (scope: %s, user: %s) iniciado por @%s. Recibirás notificación al terminar.",
				flow.Label(), scope, user, requestedBy,
			)), nil
		},
	)

	mcpAddr := fmt.Sprintf("0.0.0.0:%d", config.MCPServerPort)
	slog.Info("Starting MCP SSE server", "addr", mcpAddr)
	httpSrv := server.NewSSEServer(s, server.WithBaseURL(config.MCPBaseURL))
	if err := httpSrv.Start(mcpAddr); err != nil {
		slog.Error("MCP SSE server failed", "err", err)
		os.Exit(1)
	}
}
