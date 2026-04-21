package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/erosao/mcp-deploy/internal/config"
)

// ─── Historial de conversación ────────────────────────────────────────────────

type message struct {
	Role    string // "user", "assistant", "tool_result"
	Content string
}

type botState struct {
	mu      sync.Mutex
	history map[int64][]message
}

func newBotState() *botState {
	return &botState{history: make(map[int64][]message)}
}

func (s *botState) get(userID int64) []message {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.history[userID]
	out := make([]message, len(src))
	copy(out, src)
	return out
}

func (s *botState) set(userID int64, msgs []message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[userID] = msgs
}

func (s *botState) clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.history, userID)
}

// ─── Comunicación con MCP server ─────────────────────────────────────────────

type mcpDeployReq struct {
	Flow        string `json:"flow"`
	Scope       string `json:"scope"`
	User        string `json:"user"`
	RequestedBy string `json:"requested_by"`
	ChatID      int64  `json:"chat_id"`
}

type mcpDeployResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func callMCPDeploy(flow, scope, user, requestedBy string, chatID int64) (mcpDeployResp, error) {
	url := fmt.Sprintf("http://%s:%d/internal/deploy", config.MCPServerHost, config.MCPAPIPort)

	payload, _ := json.Marshal(mcpDeployReq{
		Flow:        flow,
		Scope:       scope,
		User:        user,
		RequestedBy: requestedBy,
		ChatID:      chatID,
	})

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return mcpDeployResp{}, fmt.Errorf("no se pudo conectar al MCP server: %w", err)
	}
	defer resp.Body.Close()

	var result mcpDeployResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return mcpDeployResp{}, fmt.Errorf("respuesta inválida del MCP server: %w", err)
	}
	return result, nil
}

// ─── Tool map — Claude llama estos, el bot los ejecuta vía MCP server ────────

type toolFunc func(args map[string]any, requestedBy string, chatID int64) string

var toolMap = map[string]toolFunc{
	"deploy": func(a map[string]any, u string, c int64) string { return execDeploy(a, u, c) },
}

func execDeploy(args map[string]any, defaultUser string, defaultChatID int64) string {
	flow, _ := args["flow"].(string)
	scope, _ := args["scope"].(string)
	user, _ := args["user"].(string)

	if flow == "" || scope == "" {
		return "Error: los parámetros 'flow' y 'scope' son requeridos"
	}

	result, err := callMCPDeploy(flow, scope, user, defaultUser, defaultChatID)
	if err != nil {
		return "Error al contactar el MCP server: " + err.Error()
	}
	if !result.Success {
		return "Error en deploy: " + result.Message
	}
	return result.Message
}

// ─── System prompt ────────────────────────────────────────────────────────────

const systemPrompt = `Eres un asistente de deploys para el servicio Facturación (FEBOL).

Tienes acceso a la herramienta "deploy" que ejecuta el script deployer.sh.

HERRAMIENTA DISPONIBLE — "deploy":
Parámetros:
  * flow (requerido): flujo de deploy. Valores posibles:
    - "miatech-to-dev"  → sincroniza Miatech → Dev (todos los pasos desde inicio)
    - "miatech-to-qa"   → sincroniza Miatech → QA (todos los pasos desde inicio)
    - "dev-to-qa"       → promueve Dev → QA (solo el paso de promoción)
    - "qa-to-prod"      → promueve QA → Producción (solo el paso de promoción) ⚠️
  * scope (requerido): alcance del deploy. Valores posibles:
    - "individual" → módulos individuales (IRead, IWrite, Portal Individual)
    - "global"     → módulos globales (CronJobs, Input, Read, Write, Timbrado, Portal)
    - "both"       → todos los módulos
  * user (opcional): usuario que ejecuta el script. Por defecto "deployer".
    Valores válidos: "franramvel", "ErosAO", "Haztel05", "deployer"

REGLA PARA USAR HERRAMIENTAS:
Cuando necesites ejecutar un deploy, responde EXACTAMENTE con este formato JSON y NADA MÁS:
{"tool": "deploy", "args": {"flow": "...", "scope": "..."}}

Ejemplos:
{"tool": "deploy", "args": {"flow": "miatech-to-dev", "scope": "global"}}
{"tool": "deploy", "args": {"flow": "dev-to-qa", "scope": "both"}}
{"tool": "deploy", "args": {"flow": "qa-to-prod", "scope": "global"}}
{"tool": "deploy", "args": {"flow": "miatech-to-dev", "scope": "individual", "user": "ErosAO"}}

REGLAS DE SEGURIDAD:
- Para "qa-to-prod" (deploy a PRODUCCIÓN): pide confirmación explícita antes de ejecutar.
- Solo ejecuta "qa-to-prod" si el usuario confirmó explícitamente en este mensaje o el inmediatamente anterior.
- Para los otros flujos: ejecuta directamente si el usuario lo solicitó con claridad.
- Si el usuario no especifica el scope, pregúntale (individual, global, o ambos).

Sé conciso. Responde siempre en el mismo idioma que el usuario.`

func buildPrompt(msgs []message, username string, chatID int64) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString(fmt.Sprintf("\n\nCONTEXTO: usuario=@%s | chat_id=%d\n", username, chatID))
	sb.WriteString("\n--- CONVERSACIÓN ---\n\n")
	for _, m := range msgs {
		switch m.Role {
		case "user":
			sb.WriteString("Usuario: ")
		case "assistant":
			sb.WriteString("Asistente: ")
		case "tool_result":
			sb.WriteString("Resultado herramienta: ")
		}
		sb.WriteString(m.Content)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nAsistente:")
	return sb.String()
}

// ─── Claude Code subprocess ───────────────────────────────────────────────────

type toolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

func callClaude(prompt string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, config.ClaudeBin, "-p", "--dangerously-skip-permissions", prompt)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude -p tardó demasiado (timeout)")
		}
		stderr := ""
		if e, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(e.Stderr))
		}
		return "", fmt.Errorf("claude -p falló: %s", stderr)
	}
	return strings.TrimSpace(string(out)), nil
}

func tryParseToolCall(text string) *toolCall {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	if start < 0 {
		return nil
	}
	end := strings.LastIndex(text, "}") + 1
	if end <= 0 {
		return nil
	}
	var tc toolCall
	if err := json.Unmarshal([]byte(text[start:end]), &tc); err != nil {
		return nil
	}
	if tc.Tool == "" || tc.Args == nil {
		return nil
	}
	return &tc
}

// ─── Teclados inline ──────────────────────────────────────────────────────────

// deployFlowKeyboard muestra los 4 flujos de deploy disponibles.
func deployFlowKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟢 Miatech → Dev",  "flow:miatech-to-dev"),
			tgbotapi.NewInlineKeyboardButtonData("🟡 Miatech → QA",   "flow:miatech-to-qa"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬆️ Dev → QA",       "flow:dev-to-qa"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔴 QA → PROD ⚠️",   "flow:qa-to-prod"),
		),
	)
}

// deployScopeKeyboard muestra los 3 alcances una vez seleccionado el flujo.
func deployScopeKeyboard(flow string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👤 Individual", fmt.Sprintf("scope:%s:individual", flow)),
			tgbotapi.NewInlineKeyboardButtonData("🌐 Global",     fmt.Sprintf("scope:%s:global", flow)),
			tgbotapi.NewInlineKeyboardButtonData("📦 Ambos",      fmt.Sprintf("scope:%s:both", flow)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Volver", "back"),
		),
	)
}

func confirmKeyboard(flow, scope string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Confirmar", fmt.Sprintf("do:%s:%s", flow, scope)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancelar",  "cancel"),
		),
	)
}

// ─── Labels ───────────────────────────────────────────────────────────────────

func flowLabel(f string) string {
	switch f {
	case "miatech-to-dev":
		return "Miatech → Dev"
	case "miatech-to-qa":
		return "Miatech → QA"
	case "dev-to-qa":
		return "Dev → QA"
	case "qa-to-prod":
		return "QA → Prod"
	default:
		return f
	}
}

func scopeLabel(s string) string {
	switch s {
	case "individual":
		return "Individual"
	case "global":
		return "Global"
	case "both":
		return "Ambos"
	default:
		return s
	}
}

// ─── Helpers Telegram ─────────────────────────────────────────────────────────

func sendHTML(b *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	b.Send(msg)
}

func sendWithKeyboard(b *tgbotapi.BotAPI, chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = kb
	b.Send(msg)
}

func replyTo(b *tgbotapi.BotAPI, chatID int64, replyToID int, text string) {
	const maxLen = 4000
	runes := []rune(text)
	for i := 0; i < len(runes); i += maxLen {
		end := i + maxLen
		if end > len(runes) {
			end = len(runes)
		}
		msg := tgbotapi.NewMessage(chatID, string(runes[i:end]))
		if i == 0 && replyToID > 0 {
			msg.ReplyToMessageID = replyToID
		}
		b.Send(msg)
	}
}

func editText(b *tgbotapi.BotAPI, chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "HTML"
	b.Send(edit)
}

func editWithKeyboard(b *tgbotapi.BotAPI, chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = &kb
	b.Send(edit)
}

func sendTyping(b *tgbotapi.BotAPI, chatID int64) {
	b.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ─── Handlers de comandos ─────────────────────────────────────────────────────

func handleCommand(b *tgbotapi.BotAPI, msg *tgbotapi.Message, state *botState) {
	switch msg.Command() {
	case "start":
		name := msg.From.FirstName
		sendHTML(b, msg.Chat.ID, fmt.Sprintf(
			"¡Hola, %s! 👋\n\n"+
				"Soy el asistente de deploys para <b>Facturación (FEBOL)</b>.\n\n"+
				"Puedes hablarme en lenguaje natural:\n"+
				"• <i>\"Haz un deploy de miatech a dev en scope global\"</i>\n"+
				"• <i>\"Promueve dev a qa con scope individual\"</i>\n"+
				"• <i>\"Deploy a producción scope both\"</i>\n\n"+
				"O usa /deploy para el menú rápido con botones.\n"+
				"Usa /help para más información.", name,
		))
	case "deploy":
		sendWithKeyboard(b, msg.Chat.ID,
			"🚀 <b>Deploy Facturación</b>\n\nSelecciona el flujo:",
			deployFlowKeyboard(),
		)
	case "help":
		sendHTML(b, msg.Chat.ID,
			"📖 <b>Comandos disponibles:</b>\n\n"+
				"/deploy — Menú rápido con botones\n"+
				"/clear — Borrar historial de conversación\n"+
				"/help — Esta ayuda\n\n"+
				"<b>Flujos disponibles:</b>\n"+
				"• <b>Miatech → Dev</b>  — sincroniza desde inicio hasta Dev\n"+
				"• <b>Miatech → QA</b>   — sincroniza desde inicio hasta QA\n"+
				"• <b>Dev → QA</b>       — promueve Dev a QA\n"+
				"• <b>QA → Prod</b>      — promueve QA a Producción ⚠️\n\n"+
				"<b>Scopes disponibles:</b>\n"+
				"• <b>Individual</b> — módulos IRead, IWrite, Portal Individual\n"+
				"• <b>Global</b>     — módulos CronJobs, Input, Read, Write, Timbrado, Portal\n"+
				"• <b>Ambos</b>      — todos los módulos\n\n"+
				"⚠️ Los deploys a <b>Prod</b> requieren confirmación explícita.",
		)
	case "clear":
		state.clear(msg.From.ID)
		sendHTML(b, msg.Chat.ID, "🗑 Historial de conversación borrado.")
	default:
		sendWithKeyboard(b, msg.Chat.ID,
			"Comando no reconocido. Usa el menú o escríbeme en lenguaje natural:",
			deployFlowKeyboard(),
		)
	}
}

// ─── Handler de callbacks (botones inline) ────────────────────────────────────

func handleCallback(b *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery, state *botState) {
	b.Request(tgbotapi.NewCallback(cb.ID, ""))

	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	username := cb.From.UserName
	if username == "" {
		username = cb.From.FirstName
	}

	// max 3 parts: action:flow:scope
	parts := strings.SplitN(cb.Data, ":", 3)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "flow":
		if len(parts) != 2 {
			return
		}
		flow := parts[1]
		editWithKeyboard(b, chatID, msgID,
			fmt.Sprintf("🚀 <b>Deploy Facturación</b>\nFlujo: <b>%s</b>\n\nSelecciona el alcance (scope):", flowLabel(flow)),
			deployScopeKeyboard(flow),
		)

	case "scope":
		if len(parts) != 3 {
			return
		}
		flow, scope := parts[1], parts[2]
		var text string
		if flow == "qa-to-prod" {
			text = fmt.Sprintf(
				"⚠️ <b>¡ATENCIÓN — Deploy a PRODUCCIÓN!</b>\n\n"+
					"Flujo : <b>%s</b>\n"+
					"Scope : <b>%s</b>\n\n"+
					"Esta acción impacta el ambiente de producción.\n"+
					"¿Confirmas el despliegue?",
				flowLabel(flow), scopeLabel(scope),
			)
		} else {
			text = fmt.Sprintf(
				"❓ <b>Confirmar Deploy</b>\n\n"+
					"Flujo : <b>%s</b>\n"+
					"Scope : <b>%s</b>\n\n"+
					"¿Deseas continuar?",
				flowLabel(flow), scopeLabel(scope),
			)
		}
		editWithKeyboard(b, chatID, msgID, text, confirmKeyboard(flow, scope))

	case "do":
		if len(parts) != 3 {
			return
		}
		flow, scope := parts[1], parts[2]

		editText(b, chatID, msgID, fmt.Sprintf(
			"⏳ Iniciando deploy <b>%s</b> (scope: %s)...", flowLabel(flow), scopeLabel(scope),
		))

		result, err := callMCPDeploy(flow, scope, "", username, chatID)
		if err != nil {
			editText(b, chatID, msgID, fmt.Sprintf(
				"❌ <b>Error al contactar el MCP server</b>\n\n<code>%s</code>", err.Error(),
			))
			return
		}
		if result.Success {
			editText(b, chatID, msgID, result.Message)
		} else {
			editText(b, chatID, msgID, fmt.Sprintf("❌ <b>Error:</b> %s", result.Message))
		}

	case "back":
		editWithKeyboard(b, chatID, msgID,
			"🚀 <b>Deploy Facturación</b>\n\nSelecciona el flujo:",
			deployFlowKeyboard(),
		)

	case "cancel":
		editText(b, chatID, msgID, "❌ Deploy cancelado.")
	}
}

// ─── Handler de mensajes de texto — agentic loop con Claude Code ──────────────

func handleTextMessage(b *tgbotapi.BotAPI, msg *tgbotapi.Message, state *botState) {
	user := msg.From
	slog.Info("user message", "userID", user.ID, "username", user.UserName,
		"text", truncate(msg.Text, 120))

	sendTyping(b, msg.Chat.ID)

	username := user.UserName
	if username == "" {
		username = user.FirstName
	}

	history := state.get(user.ID)
	history = append(history, message{Role: "user", Content: msg.Text})
	if len(history) > config.MaxHistoryMessages {
		history = history[len(history)-config.MaxHistoryMessages:]
	}

	msgs := make([]message, len(history))
	copy(msgs, history)

	const maxIterations = 8
	var finalResponse string

	for i := 0; i < maxIterations; i++ {
		sendTyping(b, msg.Chat.ID)
		slog.Info("calling claude", "iteration", i+1, "user", username)

		responseText, err := callClaude(buildPrompt(msgs, username, msg.Chat.ID), 120*time.Second)
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				replyTo(b, msg.Chat.ID, msg.MessageID,
					"⏱ La respuesta tardó demasiado. Intenta de nuevo.")
			} else {
				replyTo(b, msg.Chat.ID, msg.MessageID,
					fmt.Sprintf("❌ Error al invocar Claude: %s", err))
			}
			return
		}
		slog.Info("claude responded", "response", truncate(responseText, 200))

		tc := tryParseToolCall(responseText)
		if tc != nil {
			slog.Info("tool call", "tool", tc.Tool)
			var toolResult string
			if fn, ok := toolMap[tc.Tool]; ok {
				toolResult = fn(tc.Args, username, msg.Chat.ID)
			} else {
				toolResult = fmt.Sprintf("Error: herramienta desconocida '%s'", tc.Tool)
			}
			msgs = append(msgs, message{Role: "assistant", Content: responseText})
			msgs = append(msgs, message{
				Role:    "tool_result",
				Content: fmt.Sprintf("[Resultado de %s]: %s", tc.Tool, toolResult),
			})
		} else {
			finalResponse = responseText
			break
		}
	}

	if finalResponse == "" {
		replyTo(b, msg.Chat.ID, msg.MessageID,
			"La operación requirió demasiados pasos. Intenta con una solicitud más específica.")
		return
	}

	history = append(history, message{Role: "assistant", Content: finalResponse})
	if len(history) > config.MaxHistoryMessages {
		history = history[len(history)-config.MaxHistoryMessages:]
	}
	state.set(user.ID, history)

	replyTo(b, msg.Chat.ID, msg.MessageID, finalResponse)
}

// ─── Dispatcher de updates ────────────────────────────────────────────────────

func handleUpdate(b *tgbotapi.BotAPI, update tgbotapi.Update, state *botState) {
	if update.CallbackQuery != nil {
		cb := update.CallbackQuery
		if !config.IsAuthorized(cb.From.ID, cb.From.UserName) {
			b.Request(tgbotapi.NewCallback(cb.ID, "No autorizado"))
			return
		}
		handleCallback(b, cb, state)
		return
	}

	if update.Message == nil || update.Message.From == nil {
		return
	}
	msg := update.Message

	if !config.IsAuthorized(msg.From.ID, msg.From.UserName) {
		sendHTML(b, msg.Chat.ID, "❌ No estás autorizado para usar este bot.")
		return
	}

	if msg.IsCommand() {
		handleCommand(b, msg, state)
		return
	}

	if msg.Text != "" {
		handleTextMessage(b, msg, state)
	}
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	logFile, err := os.OpenFile(
		filepath.Join(config.LogsDir, "telegram-bot.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644,
	)
	if err != nil {
		slog.Error("cannot open log file", "err", err)
		os.Exit(1)
	}
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logFile), nil)))

	if config.TelegramBotToken == "" {
		slog.Error("TELEGRAM_BOT_TOKEN no configurado — revisa .env")
		os.Exit(1)
	}

	// Verificar Claude Code
	out, err := exec.Command(config.ClaudeBin, "--version").Output()
	if err != nil {
		slog.Error("Claude Code no encontrado", "bin", config.ClaudeBin, "err", err)
		slog.Error("Instala Claude Code: npm install -g @anthropic-ai/claude-code")
		os.Exit(1)
	}
	slog.Info("Claude Code disponible", "version", strings.TrimSpace(string(out)))

	bot, err := tgbotapi.NewBotAPI(config.TelegramBotToken)
	if err != nil {
		slog.Error("failed to create Telegram bot", "err", err)
		os.Exit(1)
	}
	slog.Info("Bot iniciado", "username", bot.Self.UserName)
	slog.Info("MCP server", "host", config.MCPServerHost, "api_port", config.MCPAPIPort)

	state := newBotState()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		go handleUpdate(bot, update, state)
	}
}
