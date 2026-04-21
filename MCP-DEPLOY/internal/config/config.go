package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	// Telegram
	TelegramBotToken    string
	AllowedUsers        []string
	NotificationChatIDs []int64

	// MCP Server (bind address + ports)
	MCPServerHost string
	MCPServerPort int
	MCPAPIPort    int

	// Bot → MCP connection
	MCPBaseURL string

	// GitHub – cuenta Miatech
	GithubMiatechToken string
	GithubMiatechOrg   string

	// GitHub – cuenta Aeromexico
	GithubAeromexicoToken string
	GithubAeromexicoOrg   string

	// Nombres de repositorios
	RepoFacturacion string
	RepoRAM         string

	// Paths
	DeployScriptPath string
	LogsDir          string
)

func init() {
	candidates := []string{
		"/opt/mcp-deploy/app/.env",
		filepath.Join(execDir(), "..", "..", ".env"),
		".env",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			_ = godotenv.Load(c)
			break
		}
	}

	TelegramBotToken = getEnv("TELEGRAM_BOT_TOKEN", "")
	MCPServerHost = getEnv("MCP_SERVER_HOST", "0.0.0.0")
	MCPServerPort = getEnvInt("MCP_SERVER_PORT", 8080)
	MCPAPIPort = getEnvInt("MCP_API_PORT", 8081)
	MCPBaseURL = getEnv("MCP_BASE_URL", fmt.Sprintf("http://127.0.0.1:%d", MCPServerPort))

	GithubMiatechToken = getEnv("GITHUB_MIATECH_TOKEN", "")
	GithubMiatechOrg = getEnv("GITHUB_MIATECH_ORG", "miatech")
	GithubAeromexicoToken = getEnv("GITHUB_AEROMEXICO_TOKEN", "")
	GithubAeromexicoOrg = getEnv("GITHUB_AEROMEXICO_ORG", "aeromexico")

	RepoFacturacion = getEnv("REPO_FACTURACION", "facturacion")
	RepoRAM = getEnv("REPO_RAM", "ram")

	DeployScriptPath = getEnv("DEPLOY_SCRIPT_PATH", "/opt/mcp-deploy/scripts/github-deploy.sh")
	LogsDir = getEnv("LOGS_DIR", defaultDir("logs"))

	if raw := getEnv("ALLOWED_TELEGRAM_USERS", ""); raw != "" {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				AllowedUsers = append(AllowedUsers, u)
			}
		}
	}

	if raw := getEnv("NOTIFICATION_CHAT_IDS", ""); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if id, err := strconv.ParseInt(s, 10, 64); err == nil {
				NotificationChatIDs = append(NotificationChatIDs, id)
			}
		}
	}

	os.MkdirAll(LogsDir, 0o755)
}

// IsAuthorized verifica si el usuario está en la lista de permitidos.
func IsAuthorized(userID int64, username string) bool {
	if len(AllowedUsers) == 0 {
		return true
	}
	uid := fmt.Sprintf("%d", userID)
	for _, u := range AllowedUsers {
		if u == uid {
			return true
		}
		if username != "" && (u == username || u == "@"+username) {
			return true
		}
	}
	return false
}

func defaultDir(name string) string {
	if _, err := os.Stat("/opt/mcp-deploy"); err == nil {
		return "/opt/mcp-deploy/" + name
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
