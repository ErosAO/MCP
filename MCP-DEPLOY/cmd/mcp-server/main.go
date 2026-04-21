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
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/erosao/mcp-deploy/internal/config"
	"github.com/erosao/mcp-deploy/internal/deploy"
	"github.com/erosao/mcp-deploy/internal/notify"
)

// strArg extrae un argumento string de la request MCP.
func strArg(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if args == nil {
		return ""
	}
	v, _ := args[key].(string)
	return v
}

// int64Arg extrae un argumento numérico (puede venir como string o float64).
func int64Arg(req mcp.CallToolRequest, key string) int64 {
	args := req.GetArguments()
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// makeDeploy genera el handler MCP para una acción de deploy específica.
func makeDeploy(svc deploy.Service, env deploy.Environment) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dr := deploy.Request{
			Service:     svc,
			Environment: env,
			RequestedBy: strArg(req, "requested_by"),
			ChatID:      int64Arg(req, "chat_id"),
		}
		go runDeploy(dr)
		return mcp.NewToolResultText(fmt.Sprintf(
			"🚀 %s iniciado por @%s. Recibirás notificación al terminar.",
			dr.DisplayName(), dr.RequestedBy,
		)), nil
	}
}

// runDeploy ejecuta el deploy y envía notificaciones Telegram en cada etapa.
func runDeploy(req deploy.Request) {
	slog.Info("deploy started", "action", req.ActionName(), "by", req.RequestedBy)

	// Construir lista única de chat IDs a notificar.
	chatSet := map[int64]struct{}{}
	if req.ChatID != 0 {
		chatSet[req.ChatID] = struct{}{}
	}
	for _, id := range config.NotificationChatIDs {
		chatSet[id] = struct{}{}
	}
	broadcast := func(text string) {
		for chatID := range chatSet {
			notify.Send(config.TelegramBotToken, chatID, text)
		}
	}

	broadcast(fmt.Sprintf(
		"⏳ <b>%s</b> en progreso...\nSolicitado por: @%s",
		req.DisplayName(), req.RequestedBy,
	))

	result := deploy.Execute(req)

	if !result.Success {
		slog.Error("deploy failed", "action", req.ActionName())
		broadcast(fmt.Sprintf(
			"❌ <b>%s</b> falló.\nSolicitado por: @%s\n\n<pre>%s</pre>",
			req.DisplayName(), req.RequestedBy, truncate(result.Message, 800),
		))
		return
	}

	slog.Info("deploy script OK", "pr_url", result.PRURL)

	msg := fmt.Sprintf(
		"✅ <b>%s</b> - Script ejecutado correctamente.\nSolicitado por: @%s",
		req.DisplayName(), req.RequestedBy,
	)
	if result.PRURL != "" {
		msg += fmt.Sprintf("\n🔗 PR: %s", result.PRURL)
	}
	broadcast(msg)

	if result.PRURL != "" {
		go deploy.MonitorPR(result.PRURL, config.GithubAeromexicoToken,
			func(success bool, notifyMsg string) {
				slog.Info("PR monitor update", "success", success)
				broadcast(notifyMsg)
			},
		)
	}
}

// ─── Internal REST API ────────────────────────────────────────────────────────

type apiDeployReq struct {
	Action      string `json:"action"`
	RequestedBy string `json:"requested_by"`
	ChatID      int64  `json:"chat_id"`
}

type apiDeployResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func parseAction(action string) (deploy.Service, deploy.Environment, error) {
	// Formato esperado: deploy_<env>_<service>
	parts := strings.SplitN(action, "_", 3)
	if len(parts) != 3 || parts[0] != "deploy" {
		return "", "", fmt.Errorf("acción inválida: %q (formato: deploy_<env>_<service>)", action)
	}
	env := deploy.Environment(parts[1])
	svc := deploy.Service(parts[2])

	validEnvs := map[deploy.Environment]bool{deploy.EnvDev: true, deploy.EnvQA: true, deploy.EnvProd: true}
	validSvcs := map[deploy.Service]bool{deploy.ServiceFacturacion: true, deploy.ServiceRAM: true}

	if !validEnvs[env] {
		return "", "", fmt.Errorf("entorno inválido: %q (opciones: dev, qa, prod)", env)
	}
	if !validSvcs[svc] {
		return "", "", fmt.Errorf("servicio inválido: %q (opciones: facturacion, ram)", svc)
	}
	return svc, env, nil
}

// startInternalAPI levanta la API HTTP interna para que el bot pueda disparar deploys.
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

		svc, env, err := parseAction(req.Action)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(apiDeployResp{false, err.Error()})
			return
		}

		dr := deploy.Request{
			Service:     svc,
			Environment: env,
			RequestedBy: req.RequestedBy,
			ChatID:      req.ChatID,
		}
		go runDeploy(dr)

		json.NewEncoder(w).Encode(apiDeployResp{
			Success: true,
			Message: fmt.Sprintf("🚀 %s iniciado. Recibirás notificación al terminar.", dr.DisplayName()),
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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

	add := func(name, desc string, svc deploy.Service, env deploy.Environment) {
		s.AddTool(
			mcp.NewTool(name,
				mcp.WithDescription(desc),
				mcp.WithString("requested_by",
					mcp.Description("Username de Telegram de quien solicita el deploy"),
				),
				mcp.WithString("chat_id",
					mcp.Description("Chat ID de Telegram para recibir notificaciones"),
				),
			),
			makeDeploy(svc, env),
		)
	}

	add("deploy_dev_facturacion",  "Deploy a Dev del servicio Facturación",  deploy.ServiceFacturacion, deploy.EnvDev)
	add("deploy_qa_facturacion",   "Deploy a QA del servicio Facturación",   deploy.ServiceFacturacion, deploy.EnvQA)
	add("deploy_prod_facturacion", "Deploy a Prod del servicio Facturación", deploy.ServiceFacturacion, deploy.EnvProd)
	add("deploy_dev_ram",          "Deploy a Dev del servicio RAM",          deploy.ServiceRAM, deploy.EnvDev)
	add("deploy_qa_ram",           "Deploy a QA del servicio RAM",           deploy.ServiceRAM, deploy.EnvQA)
	add("deploy_prod_ram",         "Deploy a Prod del servicio RAM",         deploy.ServiceRAM, deploy.EnvProd)

	mcpAddr := fmt.Sprintf("0.0.0.0:%d", config.MCPServerPort)
	slog.Info("Starting MCP SSE server", "addr", mcpAddr)
	httpSrv := server.NewSSEServer(s, server.WithBaseURL(config.MCPBaseURL))
	if err := httpSrv.Start(mcpAddr); err != nil {
		slog.Error("MCP SSE server failed", "err", err)
		os.Exit(1)
	}
}
