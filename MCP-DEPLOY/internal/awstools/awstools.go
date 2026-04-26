// Package awstools implements MCP tools for querying AWS CodePipeline and CodeBuild
// via the AWS CLI. Credentials are resolved from the environment (AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_REGION) or from the default credential chain.
package awstools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runAWS executes an AWS CLI command and returns the parsed JSON output.
func runAWS(ctx context.Context, region string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "aws", append(args, "--output", "json")...)
	if region != "" {
		cmd.Args = append(cmd.Args, "--region", region)
	}
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func statusIcon(status string) string {
	switch strings.ToUpper(status) {
	case "SUCCEEDED":
		return "✅"
	case "FAILED":
		return "❌"
	case "FAULT":
		return "⚠️"
	case "TIMED_OUT":
		return "⏱️"
	case "IN_PROGRESS":
		return "🔄"
	case "STOPPED":
		return "⛔"
	case "SUPERSEDED":
		return "⏭️"
	default:
		return "•"
	}
}

// ── Structured types for the monitor ─────────────────────────────────────────

// StageFailure holds details of a failed pipeline stage action.
type StageFailure struct {
	StageName           string
	ActionName          string
	Status              string
	ErrorCode           string
	ErrorMessage        string
	ExternalURL         string
	ExternalExecutionID string // e.g. "project-name:buildId" for CodeBuild actions
}

// IsCredentialError returns true when an AWS CLI error indicates expired or invalid credentials.
func IsCredentialError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"ExpiredTokenException",
		"InvalidClientTokenId",
		"AuthFailure",
		"InvalidSignatureException",
		"security token included in the request is expired",
		"Unable to locate credentials",
		"NoCredentialProviders",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// BuildResult holds the result of the most recent CodeBuild build.
type BuildResult struct {
	ID          string
	Status      string
	StartTime   time.Time
	FailedPhase string
	ErrorMsg    string
	LogLink     string
	Commit      string
}

// pipelineStateResponse is the raw AWS CLI shape for get-pipeline-state.
type pipelineStateResponse struct {
	PipelineName string `json:"pipelineName"`
	Updated      string `json:"updated"`
	StageStates  []struct {
		StageName       string `json:"stageName"`
		LatestExecution *struct {
			Status string `json:"status"`
		} `json:"latestExecution"`
		ActionStates []struct {
			ActionName      string `json:"actionName"`
			LatestExecution *struct {
				Status               string `json:"status"`
				ExternalExecutionId  string `json:"externalExecutionId"`
				ExternalExecutionUrl string `json:"externalExecutionUrl"`
				ErrorDetails         *struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"errorDetails"`
			} `json:"latestExecution"`
		} `json:"actionStates"`
	} `json:"stageStates"`
}

// FetchPipelineFailures returns all currently-failed stage actions for a pipeline.
func FetchPipelineFailures(ctx context.Context, pipelineName, region string) ([]StageFailure, error) {
	data, err := runAWS(ctx, region, "codepipeline", "get-pipeline-state", "--name", pipelineName)
	if err != nil {
		return nil, fmt.Errorf("get-pipeline-state(%s): %w", pipelineName, err)
	}
	var resp pipelineStateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var failures []StageFailure
	failedSet := map[string]bool{"FAILED": true, "FAULT": true, "TIMED_OUT": true}
	for _, stage := range resp.StageStates {
		for _, action := range stage.ActionStates {
			if action.LatestExecution == nil {
				continue
			}
			if !failedSet[action.LatestExecution.Status] {
				continue
			}
			f := StageFailure{
				StageName:           stage.StageName,
				ActionName:          action.ActionName,
				Status:              action.LatestExecution.Status,
				ExternalURL:         action.LatestExecution.ExternalExecutionUrl,
				ExternalExecutionID: action.LatestExecution.ExternalExecutionId,
			}
			if action.LatestExecution.ErrorDetails != nil {
				f.ErrorCode = action.LatestExecution.ErrorDetails.Code
				f.ErrorMessage = action.LatestExecution.ErrorDetails.Message
			}
			failures = append(failures, f)
		}
	}
	return failures, nil
}

// FetchLatestBuild returns the most recent build for a CodeBuild project.
func FetchLatestBuild(ctx context.Context, projectName, region string) (*BuildResult, error) {
	listData, err := runAWS(ctx, region,
		"codebuild", "list-builds-for-project",
		"--project-name", projectName,
		"--sort-order", "DESCENDING",
	)
	if err != nil {
		return nil, fmt.Errorf("list-builds-for-project(%s): %w", projectName, err)
	}
	var listResp struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(listData, &listResp); err != nil {
		return nil, fmt.Errorf("parse list: %w", err)
	}
	if len(listResp.IDs) == 0 {
		return nil, nil
	}

	batchData, err := runAWS(ctx, region, "codebuild", "batch-get-builds", "--ids", listResp.IDs[0])
	if err != nil {
		return nil, fmt.Errorf("batch-get-builds: %w", err)
	}
	var batchResp struct {
		Builds []struct {
			ID                    string `json:"id"`
			BuildStatus           string `json:"buildStatus"`
			StartTime             string `json:"startTime"`
			ResolvedSourceVersion string `json:"resolvedSourceVersion"`
			Phases                []struct {
				PhaseType   string `json:"phaseType"`
				PhaseStatus string `json:"phaseStatus"`
				Contexts    []struct {
					StatusCode string `json:"statusCode"`
					Message    string `json:"message"`
				} `json:"contexts"`
			} `json:"phases"`
			Logs struct {
				DeepLink string `json:"deepLink"`
			} `json:"logs"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(batchData, &batchResp); err != nil {
		return nil, fmt.Errorf("parse batch: %w", err)
	}
	if len(batchResp.Builds) == 0 {
		return nil, nil
	}
	b := batchResp.Builds[0]
	result := &BuildResult{
		ID:      b.ID,
		Status:  b.BuildStatus,
		LogLink: b.Logs.DeepLink,
		Commit:  b.ResolvedSourceVersion,
	}
	if t, err := time.Parse(time.RFC3339, b.StartTime); err == nil {
		result.StartTime = t
	}
	failedSet := map[string]bool{"FAILED": true, "FAULT": true, "TIMED_OUT": true}
	for _, phase := range b.Phases {
		if failedSet[phase.PhaseStatus] {
			result.FailedPhase = phase.PhaseType
			for _, ctx := range phase.Contexts {
				if ctx.Message != "" {
					result.ErrorMsg = ctx.Message
					break
				}
			}
			break
		}
	}
	return result, nil
}

// FetchBuildByID returns details of a specific CodeBuild build by its ID (format "project:buildId").
func FetchBuildByID(ctx context.Context, buildID, region string) (*BuildResult, error) {
	batchData, err := runAWS(ctx, region, "codebuild", "batch-get-builds", "--ids", buildID)
	if err != nil {
		return nil, fmt.Errorf("batch-get-builds(%s): %w", buildID, err)
	}
	var batchResp struct {
		Builds []struct {
			ID                    string `json:"id"`
			BuildStatus           string `json:"buildStatus"`
			StartTime             string `json:"startTime"`
			ResolvedSourceVersion string `json:"resolvedSourceVersion"`
			Phases                []struct {
				PhaseType   string `json:"phaseType"`
				PhaseStatus string `json:"phaseStatus"`
				Contexts    []struct {
					StatusCode string `json:"statusCode"`
					Message    string `json:"message"`
				} `json:"contexts"`
			} `json:"phases"`
			Logs struct {
				DeepLink string `json:"deepLink"`
			} `json:"logs"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(batchData, &batchResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(batchResp.Builds) == 0 {
		return nil, nil
	}
	b := batchResp.Builds[0]
	result := &BuildResult{
		ID:      b.ID,
		Status:  b.BuildStatus,
		LogLink: b.Logs.DeepLink,
		Commit:  b.ResolvedSourceVersion,
	}
	if t, err := time.Parse(time.RFC3339, b.StartTime); err == nil {
		result.StartTime = t
	}
	failedSet := map[string]bool{"FAILED": true, "FAULT": true, "TIMED_OUT": true}
	for _, phase := range b.Phases {
		if failedSet[phase.PhaseStatus] {
			result.FailedPhase = phase.PhaseType
			for _, phaseCtx := range phase.Contexts {
				if phaseCtx.Message != "" {
					result.ErrorMsg = phaseCtx.Message
					break
				}
			}
			break
		}
	}
	return result, nil
}

// ── CodePipeline ──────────────────────────────────────────────────────────────

// ListPipelines returns all CodePipelines with their last updated timestamp.
func ListPipelines(ctx context.Context, region string) (string, error) {
	data, err := runAWS(ctx, region, "codepipeline", "list-pipelines")
	if err != nil {
		return "", fmt.Errorf("aws codepipeline list-pipelines: %w", err)
	}

	var resp struct {
		Pipelines []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
			Updated string `json:"updated"`
		} `json:"pipelines"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(resp.Pipelines) == 0 {
		return "No hay CodePipelines en esta región.", nil
	}

	var sb strings.Builder
	reg := region
	if reg == "" {
		reg = "región por defecto"
	}
	fmt.Fprintf(&sb, "CodePipelines en %s (%d):\n\n", reg, len(resp.Pipelines))
	for _, p := range resp.Pipelines {
		updated := p.Updated
		if t, err := time.Parse(time.RFC3339, p.Updated); err == nil {
			updated = t.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(&sb, "• %s (v%d) — actualizado: %s\n", p.Name, p.Version, updated)
	}
	return sb.String(), nil
}

// GetPipelineState returns stage-by-stage state including error details.
func GetPipelineState(ctx context.Context, pipelineName, region string) (string, error) {
	data, err := runAWS(ctx, region, "codepipeline", "get-pipeline-state", "--name", pipelineName)
	if err != nil {
		return "", fmt.Errorf("aws codepipeline get-pipeline-state: %w", err)
	}

	var resp struct {
		PipelineName string `json:"pipelineName"`
		Updated      string `json:"updated"`
		StageStates  []struct {
			StageName       string `json:"stageName"`
			LatestExecution *struct {
				Status string `json:"status"`
			} `json:"latestExecution"`
			ActionStates []struct {
				ActionName      string `json:"actionName"`
				LatestExecution *struct {
					Status               string `json:"status"`
					ExternalExecutionUrl string `json:"externalExecutionUrl"`
					ErrorDetails         *struct {
						Message string `json:"message"`
						Code    string `json:"code"`
					} `json:"errorDetails"`
				} `json:"latestExecution"`
			} `json:"actionStates"`
		} `json:"stageStates"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Pipeline: %s\n", resp.PipelineName)
	if resp.Updated != "" {
		if t, err := time.Parse(time.RFC3339, resp.Updated); err == nil {
			fmt.Fprintf(&sb, "Última actualización: %s\n", t.Format("2006-01-02 15:04:05"))
		}
	}
	fmt.Fprintf(&sb, "\nEtapas:\n")

	for _, stage := range resp.StageStates {
		stageStatus := "PENDIENTE"
		if stage.LatestExecution != nil {
			stageStatus = stage.LatestExecution.Status
		}
		fmt.Fprintf(&sb, "\n%s [%s] %s\n", statusIcon(stageStatus), stage.StageName, stageStatus)

		for _, action := range stage.ActionStates {
			if action.LatestExecution == nil {
				fmt.Fprintf(&sb, "    • %s: —\n", action.ActionName)
				continue
			}
			as := action.LatestExecution.Status
			fmt.Fprintf(&sb, "    %s %s: %s\n", statusIcon(as), action.ActionName, as)
			if action.LatestExecution.ErrorDetails != nil {
				ed := action.LatestExecution.ErrorDetails
				fmt.Fprintf(&sb, "      Error [%s]: %s\n", ed.Code, ed.Message)
			}
			if action.LatestExecution.ExternalExecutionUrl != "" {
				fmt.Fprintf(&sb, "      URL: %s\n", action.LatestExecution.ExternalExecutionUrl)
			}
		}
	}
	return sb.String(), nil
}

// ── CodeBuild ─────────────────────────────────────────────────────────────────

// ListCodeBuildProjects returns all CodeBuild project names.
func ListCodeBuildProjects(ctx context.Context, region string) (string, error) {
	data, err := runAWS(ctx, region, "codebuild", "list-projects")
	if err != nil {
		return "", fmt.Errorf("aws codebuild list-projects: %w", err)
	}

	var resp struct {
		Projects []string `json:"projects"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(resp.Projects) == 0 {
		return "No hay proyectos de CodeBuild en esta región.", nil
	}

	reg := region
	if reg == "" {
		reg = "región por defecto"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Proyectos de CodeBuild en %s (%d):\n\n", reg, len(resp.Projects))
	for _, p := range resp.Projects {
		fmt.Fprintf(&sb, "• %s\n", p)
	}
	return sb.String(), nil
}

// GetCodeBuildBuilds returns recent builds for a project with failure details and log links.
func GetCodeBuildBuilds(ctx context.Context, projectName, region string, maxResults int) (string, error) {
	if maxResults <= 0 || maxResults > 20 {
		maxResults = 5
	}

	listData, err := runAWS(ctx, region,
		"codebuild", "list-builds-for-project",
		"--project-name", projectName,
		"--sort-order", "DESCENDING",
	)
	if err != nil {
		return "", fmt.Errorf("aws codebuild list-builds-for-project: %w", err)
	}

	var listResp struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(listData, &listResp); err != nil {
		return "", fmt.Errorf("parse list response: %w", err)
	}
	if len(listResp.IDs) == 0 {
		return fmt.Sprintf("No hay builds para el proyecto %s.", projectName), nil
	}

	ids := listResp.IDs
	if len(ids) > maxResults {
		ids = ids[:maxResults]
	}

	batchArgs := append([]string{"codebuild", "batch-get-builds", "--ids"}, ids...)
	batchData, err := runAWS(ctx, region, batchArgs...)
	if err != nil {
		return "", fmt.Errorf("aws codebuild batch-get-builds: %w", err)
	}

	var batchResp struct {
		Builds []struct {
			ID                    string `json:"id"`
			BuildStatus           string `json:"buildStatus"`
			CurrentPhase          string `json:"currentPhase"`
			StartTime             string `json:"startTime"`
			EndTime               string `json:"endTime"`
			ResolvedSourceVersion string `json:"resolvedSourceVersion"`
			Phases                []struct {
				PhaseType   string `json:"phaseType"`
				PhaseStatus string `json:"phaseStatus"`
				Contexts    []struct {
					StatusCode string `json:"statusCode"`
					Message    string `json:"message"`
				} `json:"contexts"`
			} `json:"phases"`
			Logs struct {
				DeepLink   string `json:"deepLink"`
				GroupName  string `json:"groupName"`
				StreamName string `json:"streamName"`
			} `json:"logs"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(batchData, &batchResp); err != nil {
		return "", fmt.Errorf("parse batch response: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Builds recientes — %s (mostrando %d):\n", projectName, len(batchResp.Builds))

	for _, b := range batchResp.Builds {
		startStr := b.StartTime
		endStr := b.EndTime
		if t, err := time.Parse(time.RFC3339, b.StartTime); err == nil {
			startStr = t.Format("2006-01-02 15:04:05")
		}
		if t, err := time.Parse(time.RFC3339, b.EndTime); err == nil {
			endStr = t.Format("15:04:05")
		}

		fmt.Fprintf(&sb, "\n%s %s  %s → %s\n", statusIcon(b.BuildStatus), b.BuildStatus, startStr, endStr)
		fmt.Fprintf(&sb, "  Build ID: %s\n", b.ID)
		if b.ResolvedSourceVersion != "" {
			fmt.Fprintf(&sb, "  Commit:   %s\n", b.ResolvedSourceVersion)
		}

		// Failed phase details
		failedStatuses := map[string]bool{"FAILED": true, "FAULT": true, "TIMED_OUT": true}
		if failedStatuses[b.BuildStatus] {
			for _, phase := range b.Phases {
				if failedStatuses[phase.PhaseStatus] {
					fmt.Fprintf(&sb, "  ✗ Fase fallida: %s\n", phase.PhaseType)
					for _, phaseCtx := range phase.Contexts {
						if phaseCtx.Message != "" {
							fmt.Fprintf(&sb, "    Mensaje: %s\n", phaseCtx.Message)
						}
						if phaseCtx.StatusCode != "" {
							fmt.Fprintf(&sb, "    Código: %s\n", phaseCtx.StatusCode)
						}
					}
				}
			}
		}

		// Log links
		if b.Logs.DeepLink != "" {
			fmt.Fprintf(&sb, "  Logs: %s\n", b.Logs.DeepLink)
		} else if b.Logs.GroupName != "" {
			fmt.Fprintf(&sb, "  Log Group:  %s\n", b.Logs.GroupName)
			if b.Logs.StreamName != "" {
				fmt.Fprintf(&sb, "  Log Stream: %s\n", b.Logs.StreamName)
			}
		}
	}
	return sb.String(), nil
}
