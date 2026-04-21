package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/erosao/mcp-deploy/internal/config"
)

// ─── Structs para comunicación con MCP server ─────────────────────────────────

type mcpDeployReq struct {
	Action      string `json:"action"`
	RequestedBy string `json:"requested_by"`
	ChatID      int64  `json:"chat_id"`
}

type mcpDeployResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// callMCPDeploy llama al endpoint interno del MCP server para disparar un deploy.
func callMCPDeploy(action, requestedBy string, chatID int64) (mcpDeployResp, error) {
	url := fmt.Sprintf("http://%s:%d/internal/deploy", config.MCPServerHost, config.MCPAPIPort)

	payload, _ := json.Marshal(mcpDeployReq{
		Action:      action,
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

// ─── Teclados inline ──────────────────────────────────────────────────────────

func deployKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟢 Dev Facturación",  "ask:dev:facturacion"),
			tgbotapi.NewInlineKeyboardButtonData("🟡 QA Facturación",   "ask:qa:facturacion"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔴 PROD Facturación", "ask:prod:facturacion"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟢 Dev RAM",          "ask:dev:ram"),
			tgbotapi.NewInlineKeyboardButtonData("🟡 QA RAM",           "ask:qa:ram"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔴 PROD RAM",         "ask:prod:ram"),
		),
	)
}

func confirmKeyboard(env, svc string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Confirmar", fmt.Sprintf("do:%s:%s", env, svc)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancelar",  "cancel"),
		),
	)
}

// ─── Helpers Telegram ─────────────────────────────────────────────────────────

func envEmoji(env string) string {
	switch env {
	case "dev":
		return "🟢"
	case "qa":
		return "🟡"
	case "prod":
		return "🔴"
	default:
		return "⚙️"
	}
}

func svcLabel(svc string) string {
	switch svc {
	case "facturacion":
		return "Facturación"
	case "ram":
		return "RAM"
	default:
		return strings.ToUpper(svc)
	}
}

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

// ─── Handlers ─────────────────────────────────────────────────────────────────

func handleCommand(b *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	switch msg.Command() {
	case "start", "deploy":
		sendWithKeyboard(b, msg.Chat.ID,
			"🚀 <b>MCP Deploy Bot</b>\n\nSelecciona el deploy a ejecutar:",
			deployKeyboard(),
		)
	case "help":
		sendHTML(b, msg.Chat.ID,
			"📖 <b>Comandos disponibles:</b>\n\n"+
				"/deploy — Menú de deploys\n"+
				"/help — Esta ayuda\n\n"+
				"<b>Servicios:</b>\n"+
				"• Facturación — Dev / QA / Prod\n"+
				"• RAM — Dev / QA / Prod\n\n"+
				"⚠️ Todos los deploys requieren confirmación.\n"+
				"📬 Recibirás notificación automática al terminar.",
		)
	default:
		sendWithKeyboard(b, msg.Chat.ID,
			"Usa el menú para seleccionar un deploy:",
			deployKeyboard(),
		)
	}
}

func handleCallback(b *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) {
	// Confirmar recepción del callback inmediatamente.
	b.Request(tgbotapi.NewCallback(cb.ID, ""))

	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	username := cb.From.UserName
	if username == "" {
		username = cb.From.FirstName
	}

	parts := strings.SplitN(cb.Data, ":", 3)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {

	case "ask": // Mostrar confirmación antes de ejecutar
		if len(parts) != 3 {
			return
		}
		env, svc := parts[1], parts[2]
		emoji := envEmoji(env)

		var text string
		if env == "prod" {
			text = fmt.Sprintf(
				"⚠️ <b>¡ATENCIÓN — Deploy a PRODUCCIÓN!</b>\n\n"+
					"Servicio : %s <b>%s</b>\n"+
					"Entorno  : %s <b>PROD</b>\n\n"+
					"Esta acción impacta el ambiente de producción.\n"+
					"¿Confirmas el despliegue?",
				emoji, svcLabel(svc), emoji,
			)
		} else {
			text = fmt.Sprintf(
				"❓ <b>Confirmar Deploy</b>\n\n"+
					"Servicio : %s <b>%s</b>\n"+
					"Entorno  : %s <b>%s</b>\n\n"+
					"¿Deseas continuar?",
				emoji, svcLabel(svc), emoji, strings.ToUpper(env),
			)
		}
		editWithKeyboard(b, chatID, msgID, text, confirmKeyboard(env, svc))

	case "do": // Ejecutar deploy confirmado
		if len(parts) != 3 {
			return
		}
		env, svc := parts[1], parts[2]
		action := fmt.Sprintf("deploy_%s_%s", env, svc)

		editText(b, chatID, msgID, fmt.Sprintf(
			"⏳ Iniciando <b>Deploy %s %s</b>...\n\nContactando al MCP server...",
			strings.ToUpper(env), svcLabel(svc),
		))

		result, err := callMCPDeploy(action, username, chatID)
		if err != nil {
			editText(b, chatID, msgID, fmt.Sprintf(
				"❌ <b>Error al contactar el MCP server</b>\n\n<code>%s</code>\n\n"+
					"Verifica que el servicio MCP esté activo.",
				err.Error(),
			))
			return
		}

		if result.Success {
			editText(b, chatID, msgID, result.Message)
		} else {
			editText(b, chatID, msgID, fmt.Sprintf("❌ <b>Error:</b> %s", result.Message))
		}

	case "cancel":
		editText(b, chatID, msgID, "❌ Deploy cancelado.")
	}
}

func handleUpdate(b *tgbotapi.BotAPI, update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		cb := update.CallbackQuery
		if !config.IsAuthorized(cb.From.ID, cb.From.UserName) {
			b.Request(tgbotapi.NewCallback(cb.ID, "No autorizado"))
			return
		}
		handleCallback(b, cb)
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
		handleCommand(b, msg)
		return
	}

	// Cualquier texto libre muestra el menú.
	sendWithKeyboard(b, msg.Chat.ID,
		"Usa los botones para seleccionar un deploy:",
		deployKeyboard(),
	)
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

	bot, err := tgbotapi.NewBotAPI(config.TelegramBotToken)
	if err != nil {
		slog.Error("failed to create Telegram bot", "err", err)
		os.Exit(1)
	}
	slog.Info("Bot iniciado", "username", bot.Self.UserName)
	slog.Info("MCP server endpoint", "host", config.MCPServerHost, "port", config.MCPAPIPort)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		go handleUpdate(bot, update)
	}
}
