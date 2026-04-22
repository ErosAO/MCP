package main

import (
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

	"github.com/erosao/mcp/internal/config"
	"github.com/erosao/mcp/internal/tools"
)

// ==============================================================
// Conversation history
// ==============================================================

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

// ==============================================================
// Tool map
// ==============================================================

type toolFunc func(args map[string]any) string

var toolMap = map[string]toolFunc{
	"read_file": func(args map[string]any) string {
		filename, _ := args["filename"].(string)
		return tools.ReadFile(filename)
	},
	"write_file": func(args map[string]any) string {
		filename, _ := args["filename"].(string)
		content, _ := args["content"].(string)
		return tools.WriteFile(filename, content)
	},
	"list_files": func(args map[string]any) string {
		directory, _ := args["directory"].(string)
		return tools.ListFiles(directory)
	},
	"delete_file": func(args map[string]any) string {
		filename, _ := args["filename"].(string)
		return tools.DeleteFile(filename)
	},
	"search_in_files": func(args map[string]any) string {
		query, _ := args["query"].(string)
		directory, _ := args["directory"].(string)
		return tools.SearchInFiles(query, directory)
	},
	"get_file_info": func(args map[string]any) string {
		filename, _ := args["filename"].(string)
		return tools.GetFileInfo(filename)
	},
}

// ==============================================================
// System prompt & prompt builder
// ==============================================================

const systemPrompt = `Eres un asistente inteligente con acceso a un sistema de archivos de texto en el servidor.

HERRAMIENTAS DISPONIBLES:
- read_file(filename): Lee el contenido de un archivo de texto
- write_file(filename, content): Crea o sobreescribe un archivo de texto
- list_files(directory=""): Lista archivos y directorios (directory vacío = raíz)
- delete_file(filename): Elimina un archivo o directorio
- search_in_files(query, directory=""): Busca texto en los archivos
- get_file_info(filename): Información del archivo (tamaño, fechas)

REGLA IMPORTANTE PARA USAR HERRAMIENTAS:
Cuando necesites usar una herramienta, responde EXACTAMENTE con este formato JSON y NADA MÁS:
{"tool": "nombre_herramienta", "args": {"param1": "valor1", "param2": "valor2"}}

Ejemplos:
{"tool": "list_files", "args": {}}
{"tool": "read_file", "args": {"filename": "notas.txt"}}
{"tool": "write_file", "args": {"filename": "resumen.txt", "content": "El texto del resumen..."}}
{"tool": "search_in_files", "args": {"query": "presupuesto"}}

Cuando ya tienes toda la información para responder al usuario, responde en texto normal.
Sé conciso y útil. Responde siempre en el mismo idioma que el usuario.`

func buildPrompt(msgs []message) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n--- CONVERSACIÓN ---\n\n")
	for _, m := range msgs {
		switch m.Role {
		case "user":
			sb.WriteString("Usuario: ")
		case "assistant":
			sb.WriteString("Asistente: ")
		case "tool_result":
			sb.WriteString("Resultado de herramienta: ")
		}
		sb.WriteString(m.Content)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nAsistente:")
	return sb.String()
}

// ==============================================================
// Claude subprocess
// ==============================================================

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

// ==============================================================
// Authorization
// ==============================================================

func isAuthorized(userID int64, username string) bool {
	if len(config.AllowedUsers) == 0 {
		return true
	}
	uid := fmt.Sprintf("%d", userID)
	for _, u := range config.AllowedUsers {
		if u == uid || (username != "" && u == username) {
			return true
		}
	}
	return false
}

// ==============================================================
// Telegram helpers
// ==============================================================

func reply(b *tgbotapi.BotAPI, chatID int64, replyTo int, text string) {
	const maxLen = 4000
	runes := []rune(text)
	for i := 0; i < len(runes); i += maxLen {
		end := i + maxLen
		if end > len(runes) {
			end = len(runes)
		}
		msg := tgbotapi.NewMessage(chatID, string(runes[i:end]))
		if i == 0 && replyTo > 0 {
			msg.ReplyToMessageID = replyTo
		}
		if _, err := b.Send(msg); err != nil {
			slog.Error("send message failed", "err", err)
		}
	}
}

func sendTyping(b *tgbotapi.BotAPI, chatID int64) {
	b.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
}

func deny(b *tgbotapi.BotAPI, chatID int64, replyTo int) {
	reply(b, chatID, replyTo, "Lo siento, no estás autorizado para usar este bot.\nContacta al administrador.")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ==============================================================
// Command handlers
// ==============================================================

func handleStart(b *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	name := msg.From.FirstName
	reply(b, msg.Chat.ID, msg.MessageID, fmt.Sprintf(
		"¡Hola, %s!\n\nSoy tu asistente de archivos con Claude AI.\n\n"+
			"Puedo ayudarte a:\n"+
			"• Leer archivos de texto\n"+
			"• Crear y escribir archivos\n"+
			"• Listar archivos disponibles\n"+
			"• Buscar texto en archivos\n"+
			"• Resumir contenido de archivos\n"+
			"• Eliminar archivos\n"+
			"• Guardar archivos que me envíes\n\n"+
			"Escríbeme en lenguaje natural lo que necesitas.\n"+
			"Usa /help para ver más opciones.", name,
	))
}

func handleHelp(b *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	reply(b, msg.Chat.ID, msg.MessageID,
		"Comandos disponibles:\n\n"+
			"/start — Mensaje de bienvenida\n"+
			"/help — Esta ayuda\n"+
			"/files — Listar todos los archivos\n"+
			"/clear — Borrar historial de conversación\n\n"+
			"Subir archivos:\n"+
			"Envía cualquier archivo (documento, foto, audio, video) y lo guardaré en el servidor.\n\n"+
			"Ejemplos de uso:\n"+
			"• Lee el archivo notas.txt\n"+
			"• Crea un archivo llamado tareas.txt\n"+
			"• Resume el contenido de reporte.txt\n"+
			"• Busca la palabra presupuesto en todos los archivos\n"+
			"• ¿Cuándo fue modificado datos.txt?\n"+
			"• Elimina el archivo borrador.txt",
	)
}

func handleFiles(b *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	result := tools.ListFiles("")
	reply(b, msg.Chat.ID, msg.MessageID, "Archivos en el servidor:\n\n"+result)
}

func handleClear(b *tgbotapi.BotAPI, msg *tgbotapi.Message, state *botState) {
	state.clear(msg.From.ID)
	reply(b, msg.Chat.ID, msg.MessageID, "Historial de conversación borrado.")
}

// ==============================================================
// File upload handler
// ==============================================================

func downloadTelegramFile(b *tgbotapi.BotAPI, fileID, destPath string) error {
	fc := tgbotapi.FileConfig{FileID: fileID}
	tgFile, err := b.GetFile(fc)
	if err != nil {
		return fmt.Errorf("obtener archivo: %w", err)
	}
	resp, err := http.Get(tgFile.Link(b.Token))
	if err != nil {
		return fmt.Errorf("descargar: %w", err)
	}
	defer resp.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("crear directorio: %w", err)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("crear archivo: %w", err)
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func handleFileUpload(b *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	var fileID, fileName, mimeType string
	var fileSize int

	switch {
	case msg.Document != nil:
		d := msg.Document
		fileID, fileSize, mimeType = d.FileID, d.FileSize, d.MimeType
		fileName = d.FileName
		if fileName == "" {
			fileName = "documento_" + d.FileUniqueID
		}
		if mimeType == "" {
			mimeType = "desconocido"
		}
	case len(msg.Photo) > 0:
		p := msg.Photo[len(msg.Photo)-1]
		fileID, fileSize, mimeType = p.FileID, p.FileSize, "image/jpeg"
		fileName = "foto_" + p.FileUniqueID + ".jpg"
	case msg.Audio != nil:
		a := msg.Audio
		fileID, fileSize, mimeType = a.FileID, a.FileSize, a.MimeType
		fileName = a.FileName
		if fileName == "" {
			fileName = "audio_" + a.FileUniqueID + ".mp3"
		}
		if mimeType == "" {
			mimeType = "audio"
		}
	case msg.Video != nil:
		v := msg.Video
		fileID, fileSize, mimeType = v.FileID, v.FileSize, v.MimeType
		fileName = v.FileName
		if fileName == "" {
			fileName = "video_" + v.FileUniqueID + ".mp4"
		}
		if mimeType == "" {
			mimeType = "video"
		}
	case msg.Voice != nil:
		v := msg.Voice
		fileID, fileSize, mimeType = v.FileID, v.FileSize, "audio/ogg"
		fileName = "voz_" + v.FileUniqueID + ".ogg"
	default:
		return
	}

	maxBytes := int(config.MaxFileSizeMB) * 1024 * 1024
	if fileSize > maxBytes {
		reply(b, msg.Chat.ID, msg.MessageID, fmt.Sprintf(
			"Archivo demasiado grande: %.1f MB (límite: %d MB)",
			float64(fileSize)/(1024*1024), config.MaxFileSizeMB,
		))
		return
	}

	destPath := filepath.Join(config.FilesDir, fileName)
	if err := downloadTelegramFile(b, fileID, destPath); err != nil {
		slog.Error("file download failed", "file", fileName, "err", err)
		reply(b, msg.Chat.ID, msg.MessageID, "No se pudo guardar el archivo: "+err.Error())
		return
	}

	slog.Info("file received", "name", fileName, "size", fileSize,
		"user", msg.From.ID, "username", msg.From.UserName)

	caption := ""
	if msg.Caption != "" {
		caption = "\nDescripción: " + msg.Caption
	}
	reply(b, msg.Chat.ID, msg.MessageID, fmt.Sprintf(
		"Archivo recibido y guardado en el servidor.\n\nNombre: %s\nTamaño: %s\nTipo: %s%s",
		fileName, tools.FormatSize(int64(fileSize)), mimeType, caption,
	))
}

// ==============================================================
// Text message handler (agentic loop)
// ==============================================================

func handleTextMessage(b *tgbotapi.BotAPI, msg *tgbotapi.Message, state *botState) {
	user := msg.From
	slog.Info("user message", "userID", user.ID, "username", user.UserName,
		"text", truncate(msg.Text, 120))

	sendTyping(b, msg.Chat.ID)

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
		slog.Info("calling claude", "iteration", i+1)

		responseText, err := callClaude(buildPrompt(msgs), 120*time.Second)
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				reply(b, msg.Chat.ID, msg.MessageID,
					"La respuesta tardó demasiado. Intenta de nuevo con una solicitud más corta.")
			} else {
				reply(b, msg.Chat.ID, msg.MessageID, fmt.Sprintf("Error al invocar Claude: %s", err))
			}
			return
		}
		slog.Info("claude responded", "response", truncate(responseText, 200))

		tc := tryParseToolCall(responseText)
		if tc != nil {
			slog.Info("tool call", "tool", tc.Tool)
			var toolResult string
			if fn, ok := toolMap[tc.Tool]; ok {
				toolResult = fn(tc.Args)
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
		reply(b, msg.Chat.ID, msg.MessageID,
			"La operación requirió demasiados pasos. Intenta con una solicitud más simple.")
		return
	}

	history = append(history, message{Role: "assistant", Content: finalResponse})
	if len(history) > config.MaxHistoryMessages {
		history = history[len(history)-config.MaxHistoryMessages:]
	}
	state.set(user.ID, history)

	reply(b, msg.Chat.ID, msg.MessageID, finalResponse)
}

// ==============================================================
// Update dispatcher
// ==============================================================

func handleUpdate(b *tgbotapi.BotAPI, update tgbotapi.Update, state *botState) {
	msg := update.Message
	if msg == nil || msg.From == nil {
		return
	}
	user := msg.From
	chatID := msg.Chat.ID

	if !isAuthorized(user.ID, user.UserName) {
		deny(b, chatID, msg.MessageID)
		return
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			handleStart(b, msg)
		case "help":
			handleHelp(b, msg)
		case "files":
			handleFiles(b, msg)
		case "clear":
			handleClear(b, msg, state)
		}
		return
	}

	if msg.Document != nil || len(msg.Photo) > 0 || msg.Audio != nil ||
		msg.Video != nil || msg.Voice != nil {
		handleFileUpload(b, msg)
		return
	}

	if msg.Text != "" {
		handleTextMessage(b, msg, state)
	}
}

// ==============================================================
// Main
// ==============================================================

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
		slog.Error("TELEGRAM_BOT_TOKEN no configurado. Revisa el archivo .env")
		os.Exit(1)
	}

	out, err := exec.Command(config.ClaudeBin, "--version").Output()
	if err != nil {
		slog.Error("Claude Code no encontrado", "bin", config.ClaudeBin, "err", err)
		os.Exit(1)
	}
	slog.Info("Claude Code disponible", "version", strings.TrimSpace(string(out)))

	bot, err := tgbotapi.NewBotAPI(config.TelegramBotToken)
	if err != nil {
		slog.Error("failed to create bot", "err", err)
		os.Exit(1)
	}
	slog.Info("Bot iniciado", "username", bot.Self.UserName)

	state := newBotState()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		go handleUpdate(bot, update, state)
	}
}
