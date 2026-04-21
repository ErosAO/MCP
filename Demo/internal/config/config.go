package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	FilesDir           string
	LogsDir            string
	TelegramBotToken   string
	AllowedUsers       []string
	MaxFileSizeMB      int64
	MaxHistoryMessages int
	MCPServerHost      string
	MCPServerPort      int
	ClaudeBin          string
)

func init() {
	candidates := []string{
		"/opt/mcp/app/.env",
		filepath.Join(execDir(), "..", "..", ".env"),
		".env",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			_ = godotenv.Load(c)
			break
		}
	}

	FilesDir = getEnv("FILES_DIR", defaultDir("files"))
	LogsDir = getEnv("LOGS_DIR", defaultDir("logs"))
	TelegramBotToken = getEnv("TELEGRAM_BOT_TOKEN", "")
	ClaudeBin = getEnv("CLAUDE_BIN", "claude")
	MCPServerHost = getEnv("MCP_SERVER_HOST", "127.0.0.1")
	MCPServerPort = getEnvInt("MCP_SERVER_PORT", 8000)
	MaxFileSizeMB = int64(getEnvInt("MAX_FILE_SIZE_MB", 10))
	MaxHistoryMessages = getEnvInt("MAX_HISTORY_MESSAGES", 20)

	if raw := getEnv("ALLOWED_TELEGRAM_USERS", ""); raw != "" {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				AllowedUsers = append(AllowedUsers, u)
			}
		}
	}

	os.MkdirAll(FilesDir, 0o755)
	os.MkdirAll(LogsDir, 0o755)
}

func defaultDir(name string) string {
	if _, err := os.Stat("/opt/mcp"); err == nil {
		return "/opt/mcp/" + name
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, name)
}

func execDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
