package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os/exec"
	"strings"
	"time"
)

// ─── Tipos enumerados ─────────────────────────────────────────────────────────

type Flow string
type Scope string
type User string

const (
	FlowMiatechToDev Flow = "miatech-to-dev"
	FlowMiatechToQA  Flow = "miatech-to-qa"
	FlowDevToQA      Flow = "dev-to-qa"
	FlowQAToProd     Flow = "qa-to-prod"
)

const (
	ScopeIndividual Scope = "individual"
	ScopeGlobal     Scope = "global"
	ScopeBoth       Scope = "both"
)

const (
	UserFranramvel User = "franramvel"
	UserErosAO     User = "ErosAO"
	UserHaztel05   User = "Haztel05"
	UserDeployer   User = "deployer"
)

// Label devuelve un texto legible para el flujo.
func (f Flow) Label() string {
	switch f {
	case FlowMiatechToDev:
		return "Miatech → Dev"
	case FlowMiatechToQA:
		return "Miatech → QA"
	case FlowDevToQA:
		return "Dev → QA"
	case FlowQAToProd:
		return "QA → Prod"
	default:
		return string(f)
	}
}

// IsProd indica si el flujo llega a producción.
func (f Flow) IsProd() bool { return f == FlowQAToProd }

// ─── Request / Result ─────────────────────────────────────────────────────────

type Request struct {
	Flow  Flow
	Scope Scope
	User  User
}

type Failure struct {
	Step   string `json:"step"`
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
}

type OKEntry struct {
	Repo string `json:"repo"`
	Step string `json:"step"`
}

type Summary struct {
	Failures []Failure `json:"failures"`
	OK       []OKEntry `json:"ok"`
}

type Result struct {
	ExitCode int     `json:"exit_code"`
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	Command  string  `json:"command"`
	Summary  Summary `json:"summary"`
}

// ─── Mapeo flow → flags de deployer.sh ───────────────────────────────────────

// flowFlags convierte un Flow en los flags --until y --chain del script.
func flowFlags(f Flow) (until, chain string, err error) {
	switch f {
	case FlowMiatechToDev:
		return "dev", "full", nil
	case FlowMiatechToQA:
		return "qa", "full", nil
	case FlowDevToQA:
		return "qa", "prev", nil
	case FlowQAToProd:
		return "pd", "prev", nil
	default:
		return "", "", fmt.Errorf("flow inválido: %q (opciones: miatech-to-dev | miatech-to-qa | dev-to-qa | qa-to-prod)", f)
	}
}

// ─── Ejecución ────────────────────────────────────────────────────────────────

// Execute llama a deployer.sh con los flags correctos y retorna el resultado completo.
// Timeout: 20 minutos (el deploy puede tardar varios minutos).
func Execute(req Request, scriptPath string) Result {
	until, chain, err := flowFlags(req.Flow)
	if err != nil {
		return Result{ExitCode: 1, Stderr: err.Error()}
	}

	user := string(req.User)
	if user == "" {
		user = string(UserDeployer)
	}

	args := []string{
		scriptPath,
		"--scope", string(req.Scope),
		"--user", user,
		"--until", until,
		"--chain", chain,
		"--yes",
	}
	command := "bash " + strings.Join(args, " ")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return Result{
			ExitCode: -1,
			Stdout:   stdout.String(),
			Stderr:   "timeout: el deploy tardó más de 20 minutos",
			Command:  command,
		}
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return Result{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Command:  command,
		Summary:  parseSummary(stdout.String()),
	}
}

// ─── Parser del bloque RESUMEN DEPLOY ────────────────────────────────────────

func parseSummary(output string) Summary {
	var s Summary

	start := strings.Index(output, "====== RESUMEN DEPLOY ======")
	end := strings.LastIndex(output, "============================")
	if start < 0 || end <= start {
		return s
	}

	inFail := false
	inOK := false

	for _, line := range strings.Split(output[start:end], "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "FALLOS ("):
			inFail, inOK = true, false
		case strings.Contains(trimmed, "OK por paso:"):
			inOK, inFail = true, false
		case inFail && strings.HasPrefix(trimmed, "["):
			if step, repo, reason, ok := parseFailureLine(trimmed); ok {
				s.Failures = append(s.Failures, Failure{Step: step, Repo: repo, Reason: reason})
			}
		case inOK && strings.Contains(trimmed, "|"):
			if repo, step, ok := parseOKLine(trimmed); ok {
				s.OK = append(s.OK, OKEntry{Repo: repo, Step: step})
			}
		}
	}
	return s
}

// parseFailureLine parsea "[step] repo : reason"
func parseFailureLine(line string) (step, repo, reason string, ok bool) {
	closeIdx := strings.Index(line, "]")
	if closeIdx < 0 {
		return
	}
	step = line[1:closeIdx]
	rest := strings.TrimSpace(line[closeIdx+1:])
	sepIdx := strings.Index(rest, " : ")
	if sepIdx < 0 {
		return
	}
	repo = strings.TrimSpace(rest[:sepIdx])
	reason = strings.TrimSpace(rest[sepIdx+3:])
	ok = true
	return
}

// parseOKLine parsea "repo|step"
func parseOKLine(line string) (repo, step string, ok bool) {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return
	}
	repo = strings.TrimSpace(parts[0])
	step = strings.TrimSpace(parts[1])
	ok = repo != "" && step != ""
	return
}

// ─── Formateo de salida ───────────────────────────────────────────────────────

func (r Result) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// FormatTelegram devuelve un resumen HTML para Telegram.
func (r Result) FormatTelegram(req Request) string {
	status := "✅ Completado"
	if r.ExitCode < 0 {
		status = "⏰ Timeout (>20 min)"
	} else if r.ExitCode != 0 {
		status = fmt.Sprintf("❌ Fallido (exit %d)", r.ExitCode)
	} else if len(r.Summary.Failures) > 0 {
		status = fmt.Sprintf("⚠️ Con %d fallo(s)", len(r.Summary.Failures))
	}

	msg := fmt.Sprintf("<b>%s — %s</b>\n", status, req.Flow.Label())
	msg += fmt.Sprintf("Scope: <b>%s</b>\n", req.Scope)

	if len(r.Summary.OK) > 0 {
		msg += fmt.Sprintf("\n✅ <b>OK (%d):</b>\n", len(r.Summary.OK))
		for _, o := range r.Summary.OK {
			msg += fmt.Sprintf("  • %s [%s]\n", html.EscapeString(o.Repo), html.EscapeString(o.Step))
		}
	}

	if len(r.Summary.Failures) > 0 {
		msg += fmt.Sprintf("\n❌ <b>FALLOS (%d):</b>\n", len(r.Summary.Failures))
		for _, f := range r.Summary.Failures {
			msg += fmt.Sprintf("  • [%s] %s: %s\n", html.EscapeString(f.Step), html.EscapeString(f.Repo), html.EscapeString(f.Reason))
		}
	}

	return strings.TrimRight(msg, "\n")
}
