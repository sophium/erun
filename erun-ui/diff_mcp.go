package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	mcppkg "github.com/sophium/erun/erun-common/infra/mcp"
)

type idleProbeRoundTripper struct {
	base http.RoundTripper
}

func (t idleProbeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(mcppkg.MCPIdleProbeHeader, "true")
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// mcpAuthRoundTripper adds the per-env bearer; an empty token deliberately sends none.
type mcpAuthRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t mcpAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// mcpClientTransport uses the idle-probe round-tripper so diagnostic reads do not keep an env awake.
func mcpClientTransport(endpoint, bearer string, idleProbe bool) *mcp.StreamableClientTransport {
	var base http.RoundTripper
	if idleProbe {
		base = idleProbeRoundTripper{}
	}
	return &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: mcpAuthRoundTripper{token: bearer, base: base},
		},
		DisableStandaloneSSE: true,
	}
}

func loadDiffFromMCP(ctx context.Context, endpoint, bearer string, options uiDiffOptions) (model.DiffResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return model.DiffResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "diff",
		Arguments: map[string]any{
			"scope":          strings.TrimSpace(options.Scope),
			"selectedCommit": strings.TrimSpace(options.SelectedCommit),
			"target":         strings.TrimSpace(options.Target),
		},
	})
	if err != nil {
		return model.DiffResult{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return model.DiffResult{}, err
	}
	var diff model.DiffResult
	if err := json.Unmarshal(data, &diff); err != nil {
		return model.DiffResult{}, err
	}
	return diff, nil
}

func setEnvironmentCloudAliasViaMCP(ctx context.Context, endpoint, bearer, tenant, environment, alias string) (config.EnvConfig, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return config.EnvConfig{}, err
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
		return config.EnvConfig{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return config.EnvConfig{}, err
	}
	var output struct {
		EnvConfig config.EnvConfig `json:"envConfig"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return config.EnvConfig{}, err
	}
	return output.EnvConfig, nil
}

// runPodRawFromMCP uses idle-probe reads so hovers and Diagnostics do not keep an env awake.
func runPodRawFromMCP(ctx context.Context, endpoint, bearer string, argv []string) (string, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "raw",
		Arguments: map[string]any{
			"command": argv,
		},
	})
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return "", err
	}
	var output struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return "", err
	}
	return output.Stdout, nil
}

func loadPodBranchFromMCP(ctx context.Context, endpoint, bearer string) (string, error) {
	out, err := runPodRawFromMCP(ctx, endpoint, bearer, []string{"git", "rev-parse", "--abbrev-ref", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func loadIdleStatusFromMCP(ctx context.Context, endpoint, bearer string) (model.EnvironmentIdleStatus, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "idle"})
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	var status model.EnvironmentIdleStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return model.EnvironmentIdleStatus{}, err
	}
	return status, nil
}

// apiLogMCPRawCommand mirrors loadAPILogFromKubernetes for the MCP `raw` fallback; the pod
// resolves its own namespace. It selects <tenant>-api by label with --prefix so each line names its replica.
const apiLogMCPRawCommand = `namespace=${ERUN_NAMESPACE:-}; if [ -z "$namespace" ] && [ -r /var/run/secrets/kubernetes.io/serviceaccount/namespace ]; then namespace=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace); fi; kubectl --context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}" --namespace "$namespace" logs -l app="${ERUN_TENANT:-erun}-api" --prefix -c erun-backend-api --tail 400`

func loadAPILogFromMCP(ctx context.Context, endpoint, bearer string) (string, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "raw",
		Arguments: map[string]any{
			"command": []string{"sh", "-lc", apiLogMCPRawCommand},
		},
	})
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return "", err
	}
	var output struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return "", err
	}
	log := strings.TrimRight(output.Stdout, "\n")
	if stderr := strings.TrimSpace(output.Stderr); stderr != "" {
		if log != "" {
			log += "\n"
		}
		log += stderr
	}
	return log, nil
}
