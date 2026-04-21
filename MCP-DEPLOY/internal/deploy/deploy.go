package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/erosao/mcp-deploy/internal/config"
)

type Service string
type Environment string

const (
	ServiceFacturacion Service = "facturacion"
	ServiceRAM         Service = "ram"

	EnvDev  Environment = "dev"
	EnvQA   Environment = "qa"
	EnvProd Environment = "prod"
)

type Request struct {
	Service     Service
	Environment Environment
	RequestedBy string
	ChatID      int64
}

type Result struct {
	Success bool
	Message string
	PRURL   string
}

func (r Request) ActionName() string {
	return fmt.Sprintf("deploy_%s_%s", r.Environment, r.Service)
}

func (r Request) DisplayName() string {
	return fmt.Sprintf("Deploy %s %s", strings.ToUpper(string(r.Environment)), capitalize(string(r.Service)))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Execute corre el script bash de deploy pasando ambiente y servicio.
// El script debe imprimir "PR_URL: https://github.com/..." para que se extraiga la URL.
func Execute(req Request) Result {
	scriptPath := config.DeployScriptPath

	if _, err := os.Stat(scriptPath); err != nil {
		return Result{
			Success: false,
			Message: fmt.Sprintf("Script no encontrado en %s — verifica DEPLOY_SCRIPT_PATH", scriptPath),
		}
	}

	cmd := exec.Command("bash", scriptPath, string(req.Environment), string(req.Service))

	// Pasar tokens y configuración como variables de entorno al script.
	cmd.Env = append(os.Environ(),
		"GITHUB_MIATECH_TOKEN="+config.GithubMiatechToken,
		"GITHUB_MIATECH_ORG="+config.GithubMiatechOrg,
		"GITHUB_AEROMEXICO_TOKEN="+config.GithubAeromexicoToken,
		"GITHUB_AEROMEXICO_ORG="+config.GithubAeromexicoOrg,
		"REPO_FACTURACION="+config.RepoFacturacion,
		"REPO_RAM="+config.RepoRAM,
		"REQUESTED_BY="+req.RequestedBy,
	)

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		return Result{
			Success: false,
			Message: fmt.Sprintf("Script falló: %s\n\n%s", err.Error(), truncate(output, 1000)),
		}
	}

	return Result{
		Success: true,
		Message: output,
		PRURL:   extractPRURL(output),
	}
}

// extractPRURL busca "PR_URL: https://..." o una URL de PR de GitHub en el output.
func extractPRURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "PR_URL:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "PR_URL:"))
		}
		if strings.Contains(line, "github.com") && strings.Contains(line, "/pull/") {
			for _, word := range strings.Fields(line) {
				if strings.HasPrefix(word, "https://github.com") && strings.Contains(word, "/pull/") {
					return strings.TrimRight(word, ".,;)")
				}
			}
		}
	}
	return ""
}

// MonitorPR hace polling al PR de GitHub y llama notifyFn con el resultado final.
// Polling cada 30 segundos, timeout de 2 horas.
func MonitorPR(prURL, githubToken string, notifyFn func(success bool, msg string)) {
	if prURL == "" {
		return
	}

	// Parsear: https://github.com/{owner}/{repo}/pull/{number}
	clean := strings.TrimRight(prURL, "/")
	parts := strings.Split(clean, "/")
	if len(parts) < 7 {
		notifyFn(false, fmt.Sprintf("⚠️ No se pudo parsear la URL del PR: %s", prURL))
		return
	}
	owner := parts[3]
	repo := parts[4]
	prNum := parts[6]

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	timeout := time.After(2 * time.Hour)

	for {
		select {
		case <-timeout:
			notifyFn(false, fmt.Sprintf(
				"⏰ <b>Timeout monitoreando PR #%s</b>\nVerifica manualmente: %s", prNum, prURL,
			))
			return
		case <-ticker.C:
			state, merged, err := checkPRStatus(owner, repo, prNum, githubToken)
			if err != nil {
				// Error transitorio — seguir intentando
				continue
			}
			if state == "closed" {
				if merged {
					notifyFn(true, fmt.Sprintf(
						"✅ <b>PR #%s mergeado exitosamente</b>\n%s", prNum, prURL,
					))
				} else {
					notifyFn(false, fmt.Sprintf(
						"❌ <b>PR #%s cerrado sin merge</b>\nDeploy cancelado.\n%s", prNum, prURL,
					))
				}
				return
			}
		}
	}
}

type githubPRResp struct {
	State  string `json:"state"`
	Merged bool   `json:"merged"`
}

func checkPRStatus(owner, repo, prNum, token string) (state string, merged bool, err error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", owner, repo, prNum)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	var pr githubPRResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", false, err
	}
	return pr.State, pr.Merged, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
