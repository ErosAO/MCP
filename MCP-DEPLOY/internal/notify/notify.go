package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type telegramMsg struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// Send envía un mensaje HTML a un chat de Telegram vía Bot API.
func Send(botToken string, chatID int64, text string) {
	if botToken == "" || chatID == 0 {
		return
	}

	payload, _ := json.Marshal(telegramMsg{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	})

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		slog.Error("telegram notify failed", "chatID", chatID, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Error("telegram notify HTTP error", "chatID", chatID, "status", resp.StatusCode)
	}
}
