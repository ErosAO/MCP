// Package monitor polls AWS CodePipeline and CodeBuild at a configurable interval
// and sends Telegram alerts when failures or credential expiry are detected.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/erosao/mcp-deploy/internal/awstools"
	"github.com/erosao/mcp-deploy/internal/config"
	"github.com/erosao/mcp-deploy/internal/notify"
)

// Start launches the background monitor goroutine.
// Returns immediately if no pipelines or CodeBuild projects are configured.
func Start(ctx context.Context) {
	hasPipelines := len(config.MonitorPipelines) > 0
	hasProjects := len(config.MonitorCodeBuild) > 0
	if !hasPipelines && !hasProjects {
		slog.Info("AWS monitor disabled — set AWS_MONITOR_PIPELINES or AWS_MONITOR_CODEBUILD to enable")
		return
	}
	slog.Info("AWS monitor started",
		"pipelines", config.MonitorPipelines,
		"codebuild", config.MonitorCodeBuild,
		"interval", config.MonitorInterval,
		"region", config.AWSRegion,
	)
	go run(ctx)
}

// ── state ─────────────────────────────────────────────────────────────────────

var (
	mu sync.Mutex

	// knownFailures tracks failures already reported to avoid spam.
	// key: "pipeline|name|stage|action" or "codebuild|project|buildID"
	// value: last error message sent (resend if it changes)
	knownFailures = map[string]string{}

	// credExpired is true while credentials are known to be expired.
	credExpired bool
	// lastCredAlert is the time the last credential-expiry alert was sent.
	lastCredAlert time.Time
)

// ── loop ──────────────────────────────────────────────────────────────────────

func run(ctx context.Context) {
	tick := time.NewTicker(config.MonitorInterval)
	defer tick.Stop()

	check(ctx) // run immediately on startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check(ctx)
		}
	}
}

func check(ctx context.Context) {
	for _, pipeline := range config.MonitorPipelines {
		checkPipeline(ctx, pipeline)
	}
	for _, project := range config.MonitorCodeBuild {
		checkCodeBuild(ctx, project)
	}
}

// ── pipeline monitor ──────────────────────────────────────────────────────────

func checkPipeline(ctx context.Context, pipelineName string) {
	failures, err := awstools.FetchPipelineFailures(ctx, pipelineName, config.AWSRegion)
	if err != nil {
		if awstools.IsCredentialError(err) {
			handleCredentialError()
		} else {
			slog.Error("monitor: FetchPipelineFailures", "pipeline", pipelineName, "err", err)
		}
		return
	}
	handleCredentialRestored()

	// Determine which failures are new (outside the lock to allow API calls below).
	mu.Lock()
	type newFailure struct {
		key string
		f   awstools.StageFailure
	}
	var toReport []newFailure
	reportedKeys := map[string]bool{}

	for _, f := range failures {
		key := fmt.Sprintf("pipeline|%s|%s|%s", pipelineName, f.StageName, f.ActionName)
		reportedKeys[key] = true
		if prev, seen := knownFailures[key]; seen && prev == f.ErrorMessage {
			continue // same failure already reported
		}
		toReport = append(toReport, newFailure{key, f})
	}
	mu.Unlock()

	// For each new failure, optionally enrich with CodeBuild details, then notify.
	for _, nr := range toReport {
		var build *awstools.BuildResult
		// If the external execution ID looks like a CodeBuild ID ("project:buildId"),
		// fetch build details to include phase errors and CloudWatch log link.
		if id := nr.f.ExternalExecutionID; strings.Contains(id, ":") {
			if br, err := awstools.FetchBuildByID(ctx, id, config.AWSRegion); err == nil {
				build = br
			}
		}
		broadcast(formatPipelineAlert(pipelineName, nr.f, build))
	}

	// Persist new failure states and clear resolved ones.
	mu.Lock()
	for _, nr := range toReport {
		knownFailures[nr.key] = nr.f.ErrorMessage
	}
	for key := range knownFailures {
		if strings.HasPrefix(key, "pipeline|"+pipelineName+"|") && !reportedKeys[key] {
			delete(knownFailures, key)
		}
	}
	mu.Unlock()
}

// ── standalone CodeBuild monitor (for projects not tied to a pipeline) ────────

func checkCodeBuild(ctx context.Context, projectName string) {
	build, err := awstools.FetchLatestBuild(ctx, projectName, config.AWSRegion)
	if err != nil {
		if awstools.IsCredentialError(err) {
			handleCredentialError()
		} else {
			slog.Error("monitor: FetchLatestBuild", "project", projectName, "err", err)
		}
		return
	}
	handleCredentialRestored()

	if build == nil {
		return
	}

	failedSet := map[string]bool{"FAILED": true, "FAULT": true, "TIMED_OUT": true}
	if !failedSet[build.Status] {
		mu.Lock()
		for key := range knownFailures {
			if strings.HasPrefix(key, "codebuild|"+projectName+"|") {
				delete(knownFailures, key)
			}
		}
		mu.Unlock()
		return
	}

	key := fmt.Sprintf("codebuild|%s|%s", projectName, build.ID)
	mu.Lock()
	_, alreadySent := knownFailures[key]
	if !alreadySent {
		knownFailures[key] = build.Status
	}
	mu.Unlock()

	if !alreadySent {
		broadcast(formatBuildAlert(projectName, build))
	}
}

// ── credential expiry handling ────────────────────────────────────────────────

func handleCredentialError() {
	mu.Lock()
	defer mu.Unlock()

	// Avoid spam: re-send at most once every 30 minutes while expired.
	if credExpired && time.Since(lastCredAlert) < 30*time.Minute {
		return
	}
	credExpired = true
	lastCredAlert = time.Now()

	slog.Warn("AWS credentials expired or invalid — sending Telegram alert")
	broadcast(
		"🔑 <b>Credenciales AWS expiradas</b>\n\n" +
			"Las credenciales SSO han vencido. El monitor está pausado.\n\n" +
			"<b>Para renovar:</b>\n" +
			"1. Obtén nuevas credenciales del portal SSO de AWS\n" +
			"2. Edita <code>/opt/mcp-deploy/app/.env</code> con los nuevos valores:\n" +
			"   <code>AWS_ACCESS_KEY_ID</code>\n" +
			"   <code>AWS_SECRET_ACCESS_KEY</code>\n" +
			"   <code>AWS_SESSION_TOKEN</code>\n" +
			"3. Reinicia el servicio:\n" +
			"   <code>sudo systemctl restart mcp-server</code>",
	)
}

func handleCredentialRestored() {
	mu.Lock()
	wasExpired := credExpired
	credExpired = false
	mu.Unlock()

	if wasExpired {
		slog.Info("AWS credentials valid — monitor resumed")
		broadcast("✅ <b>Credenciales AWS renovadas</b>\nEl monitor de pipelines está activo nuevamente.")
	}
}

// ── broadcast ─────────────────────────────────────────────────────────────────

func broadcast(text string) {
	for _, chatID := range config.NotificationChatIDs {
		notify.Send(config.TelegramBotToken, chatID, text)
	}
}

// ── Telegram message formatting ───────────────────────────────────────────────

func formatPipelineAlert(pipelineName string, f awstools.StageFailure, build *awstools.BuildResult) string {
	icon := "❌"
	if f.Status == "TIMED_OUT" {
		icon = "⏱️"
	} else if f.Status == "FAULT" {
		icon = "⚠️"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s <b>Pipeline fallido: %s</b>\n\n", icon, escapeHTML(pipelineName))
	fmt.Fprintf(&sb, "Etapa: <b>%s</b>  ·  Acción: <b>%s</b>\n", escapeHTML(f.StageName), escapeHTML(f.ActionName))

	if f.ErrorMessage != "" {
		code := ""
		if f.ErrorCode != "" {
			code = " [" + f.ErrorCode + "]"
		}
		fmt.Fprintf(&sb, "Error%s: <code>%s</code>\n", escapeHTML(code), escapeHTML(f.ErrorMessage))
	}

	// CodeBuild enrichment — only shown when we successfully fetched build details.
	if build != nil {
		if build.FailedPhase != "" {
			fmt.Fprintf(&sb, "\n🔨 <b>CodeBuild</b>\n")
			fmt.Fprintf(&sb, "Fase fallida: <b>%s</b>\n", escapeHTML(build.FailedPhase))
			if build.ErrorMsg != "" {
				fmt.Fprintf(&sb, "Detalle: <code>%s</code>\n", escapeHTML(build.ErrorMsg))
			}
		}
		if build.Commit != "" {
			short := build.Commit
			if len(short) > 8 {
				short = short[:8]
			}
			fmt.Fprintf(&sb, "Commit: <code>%s</code>\n", escapeHTML(short))
		}
		if build.LogLink != "" {
			fmt.Fprintf(&sb, "🔗 <a href=\"%s\">Ver logs en CloudWatch</a>\n", build.LogLink)
		}
	} else if f.ExternalURL != "" {
		fmt.Fprintf(&sb, "🔗 <a href=\"%s\">Ver en AWS Console</a>\n", f.ExternalURL)
	}

	fmt.Fprintf(&sb, "\n<i>🕐 %s UTC</i>", time.Now().UTC().Format("2006-01-02 15:04"))
	return sb.String()
}

func formatBuildAlert(projectName string, b *awstools.BuildResult) string {
	icon := "❌"
	if b.Status == "TIMED_OUT" {
		icon = "⏱️"
	} else if b.Status == "FAULT" {
		icon = "⚠️"
	}

	buildShort := b.ID
	if parts := strings.SplitN(b.ID, ":", 2); len(parts) == 2 {
		buildShort = parts[1]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s <b>Build fallido: %s</b>\n\n", icon, escapeHTML(projectName))
	fmt.Fprintf(&sb, "Build: <code>%s</code>\n", escapeHTML(buildShort))
	if !b.StartTime.IsZero() {
		fmt.Fprintf(&sb, "Inicio: %s UTC\n", b.StartTime.UTC().Format("2006-01-02 15:04"))
	}
	if b.FailedPhase != "" {
		fmt.Fprintf(&sb, "Fase fallida: <b>%s</b>\n", escapeHTML(b.FailedPhase))
	}
	if b.ErrorMsg != "" {
		fmt.Fprintf(&sb, "Detalle: <code>%s</code>\n", escapeHTML(b.ErrorMsg))
	}
	if b.Commit != "" {
		short := b.Commit
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Fprintf(&sb, "Commit: <code>%s</code>\n", escapeHTML(short))
	}
	if b.LogLink != "" {
		fmt.Fprintf(&sb, "🔗 <a href=\"%s\">Ver logs en CloudWatch</a>\n", b.LogLink)
	}
	fmt.Fprintf(&sb, "\n<i>🕐 %s UTC</i>", time.Now().UTC().Format("2006-01-02 15:04"))
	return sb.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
