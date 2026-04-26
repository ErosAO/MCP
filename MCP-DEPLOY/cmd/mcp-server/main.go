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

	"github.com/erosao/mcp-deploy/internal/awstools"
	"github.com/erosao/mcp-deploy/internal/config"
	"github.com/erosao/mcp-deploy/internal/deploy"
	"github.com/erosao/mcp-deploy/internal/monitor"
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
		"⏳ <b>Deploy %s</b> en progreso...\nScope: <b>%s</b>\nSolicitado por: @%s",
		dreq.Flow.Label(), dreq.Scope, requestedBy,
	))

	result := deploy.Execute(dreq, config.DeployScriptPath)

	slog.Info("deploy finished", "exit_code", result.ExitCode, "failures", len(result.Summary.Failures))
	if result.ExitCode != 0 {
		slog.Error("deploy script output", "stdout", result.Stdout, "stderr", result.Stderr)
	}
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

		dreq := deploy.Request{
			Flow:  deploy.Flow(req.Flow),
			Scope: deploy.Scope(req.Scope),
			User:  deploy.UserDeployer,
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

	// Monitor de AWS CodePipeline / CodeBuild
	monitor.Start(context.Background())

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
			requestedBy := strArg(req, "requested_by")

			var chatID int64
			if s := strArg(req, "chat_id"); s != "" {
				chatID, _ = strconv.ParseInt(s, 10, 64)
			}

			dreq := deploy.Request{Flow: flow, Scope: scope, User: deploy.UserDeployer}
			go runDeploy(dreq, requestedBy, chatID)

			return mcp.NewToolResultText(fmt.Sprintf(
				"🚀 Deploy %s (scope: %s) iniciado por @%s. Recibirás notificación al terminar.",
				flow.Label(), scope, requestedBy,
			)), nil
		},
	)

	// ── AWS CodePipeline tools ────────────────────────────────────────────────

	s.AddTool(
		mcp.NewTool("list_pipelines",
			mcp.WithDescription("Lista todos los CodePipelines de AWS con su fecha de última actualización."),
			mcp.WithString("region",
				mcp.Description("Región de AWS (ej: us-east-1). Si se omite usa AWS_REGION o la región por defecto."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := awstools.ListPipelines(ctx, strArg(req, "region"))
			if err != nil {
				return mcp.NewToolResultText("Error: " + err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_pipeline_state",
			mcp.WithDescription("Muestra el estado actual de cada etapa (stage) y acción de un CodePipeline, incluyendo errores detallados si hay fallos."),
			mcp.WithString("pipeline_name",
				mcp.Required(),
				mcp.Description("Nombre del CodePipeline a consultar."),
			),
			mcp.WithString("region",
				mcp.Description("Región de AWS. Si se omite usa AWS_REGION o la región por defecto."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := awstools.GetPipelineState(ctx, strArg(req, "pipeline_name"), strArg(req, "region"))
			if err != nil {
				return mcp.NewToolResultText("Error: " + err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// ── AWS CodeBuild tools ───────────────────────────────────────────────────

	s.AddTool(
		mcp.NewTool("list_codebuild_projects",
			mcp.WithDescription("Lista todos los proyectos de AWS CodeBuild disponibles en la región."),
			mcp.WithString("region",
				mcp.Description("Región de AWS. Si se omite usa AWS_REGION o la región por defecto."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := awstools.ListCodeBuildProjects(ctx, strArg(req, "region"))
			if err != nil {
				return mcp.NewToolResultText("Error: " + err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_codebuild_builds",
			mcp.WithDescription("Muestra los builds recientes de un proyecto CodeBuild. Para builds fallidos incluye la fase que falló, el mensaje de error y el enlace a los logs de CloudWatch."),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("Nombre del proyecto de CodeBuild."),
			),
			mcp.WithString("region",
				mcp.Description("Región de AWS. Si se omite usa AWS_REGION o la región por defecto."),
			),
			mcp.WithString("max_results",
				mcp.Description("Número máximo de builds a mostrar (1-20, default: 5)."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			maxResults := 5
			if s := strArg(req, "max_results"); s != "" {
				if n, err := strconv.Atoi(s); err == nil {
					maxResults = n
				}
			}
			result, err := awstools.GetCodeBuildBuilds(ctx, strArg(req, "project_name"), strArg(req, "region"), maxResults)
			if err != nil {
				return mcp.NewToolResultText("Error: " + err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
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
