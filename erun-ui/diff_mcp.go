package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type idleProbeRoundTripper struct {
	base http.RoundTripper
}

func (t idleProbeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Erun-Idle-Probe", "true")
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func loadDiffFromMCP(ctx context.Context, endpoint string, options uiDiffOptions) (eruncommon.DiffResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return eruncommon.DiffResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "diff",
		Arguments: map[string]any{
			"scope":          strings.TrimSpace(options.Scope),
			"selectedCommit": strings.TrimSpace(options.SelectedCommit),
		},
	})
	if err != nil {
		return eruncommon.DiffResult{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.DiffResult{}, err
	}
	var diff eruncommon.DiffResult
	if err := json.Unmarshal(data, &diff); err != nil {
		return eruncommon.DiffResult{}, err
	}
	return diff, nil
}

func setEnvironmentCloudAliasViaMCP(ctx context.Context, endpoint, tenant, environment, alias string) (eruncommon.EnvConfig, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return eruncommon.EnvConfig{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "cloud_set",
		Arguments: map[string]any{
			"tenant":      tenant,
			"environment": environment,
			"alias":       alias,
		},
	})
	if err != nil {
		return eruncommon.EnvConfig{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.EnvConfig{}, err
	}
	var output struct {
		EnvConfig eruncommon.EnvConfig `json:"envConfig"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return eruncommon.EnvConfig{}, err
	}
	return output.EnvConfig, nil
}

func loadIdleStatusFromMCP(ctx context.Context, endpoint string) (eruncommon.EnvironmentIdleStatus, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: idleProbeRoundTripper{},
		},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return eruncommon.EnvironmentIdleStatus{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "idle"})
	if err != nil {
		return eruncommon.EnvironmentIdleStatus{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.EnvironmentIdleStatus{}, err
	}
	var status eruncommon.EnvironmentIdleStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return eruncommon.EnvironmentIdleStatus{}, err
	}
	return status, nil
}
