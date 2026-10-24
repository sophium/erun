package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// loadEnvironmentJobsFromMCP lists an environment's retained jobs from its own
// pod, the only place a remote-agent/runtime env's jobs actually run. Reads
// are idle-probed so watching the Jobs tab never holds an otherwise-idle env
// awake.
func loadEnvironmentJobsFromMCP(ctx context.Context, endpoint, bearer string) ([]shared.EnvironmentJob, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "job_status"})
	if err != nil {
		return nil, formatJobMCPError("job_status", err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Jobs []shared.EnvironmentJob `json:"jobs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Jobs == nil {
		return []shared.EnvironmentJob{}, nil
	}
	return payload.Jobs, nil
}

// readEnvironmentJobOutputFromMCP pages through one job's captured output on
// the pod that ran it.
func readEnvironmentJobOutputFromMCP(ctx context.Context, endpoint, bearer string, params jobs.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return jobs.EnvironmentJobOutput{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "job_output",
		Arguments: map[string]any{
			"id":       params.ID,
			"offset":   params.Offset,
			"maxBytes": params.MaxBytes,
		},
	})
	if err != nil {
		return jobs.EnvironmentJobOutput{}, formatJobMCPError("job_output", err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return jobs.EnvironmentJobOutput{}, err
	}
	var output jobs.EnvironmentJobOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return jobs.EnvironmentJobOutput{}, err
	}
	return output, nil
}

// cancelEnvironmentJobFromMCP signals the job's work on the pod that runs it.
// Not idle-probed: cancelling a job is a real action, not a diagnostic read.
func cancelEnvironmentJobFromMCP(ctx context.Context, endpoint, bearer string, params jobs.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return jobs.CancelEnvironmentJobResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "job_cancel",
		Arguments: map[string]any{
			"id":     params.ID,
			"signal": params.Signal,
		},
	})
	if err != nil {
		return jobs.CancelEnvironmentJobResult{}, formatJobMCPError("job_cancel", err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return jobs.CancelEnvironmentJobResult{}, err
	}
	var payload struct {
		Cancel jobs.CancelEnvironmentJobResult `json:"cancel"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return jobs.CancelEnvironmentJobResult{}, err
	}
	return payload.Cancel, nil
}

// formatJobMCPError translates the "runtime image predates the job_* MCP
// tools" failure into one user-facing line pointing at the fix, mirroring
// formatIdleStopMCPError. Any other error passes through raw so a real
// cluster-side failure still reaches the user.
func formatJobMCPError(tool string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "method not found"),
		strings.Contains(lower, "unknown method"),
		strings.Contains(lower, "unknown tool"),
		strings.Contains(lower, "tool not found"),
		strings.Contains(lower, "-32601"),
		strings.Contains(lower, "no such tool"):
		return fmt.Errorf(
			"%s: this environment's runtime image is too old to list jobs. "+
				"Rebuild and redeploy the env (erun deploy <tenant> <env>) so the pod runs "+
				"a runtime image that includes the job_* MCP tools",
			tool,
		)
	}
	return fmt.Errorf("%s: %s", tool, msg)
}
