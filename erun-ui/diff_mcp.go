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

// mcpAuthRoundTripper stamps the per-env bearer onto every MCP request so an
// auth-enabled env edge accepts the call. An empty token is a
// no-op, so non-auth envs and unit tests (which sign no token) keep working;
// an auth-enabled env rejects the empty bearer with 401, which is correct.
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

// mcpClientTransport builds the shared StreamableClientTransport every desktop
// MCP call uses: it always carries the per-env bearer (mcpAuthRoundTripper) and,
// for diagnostic reads, wraps the idle-probe header round-tripper underneath so
// the call does not register as activity and hold an idle env awake.
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

func loadDiffFromMCP(ctx context.Context, endpoint, bearer string, options uiDiffOptions) (eruncommon.DiffResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
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
			"target":         strings.TrimSpace(options.Target),
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

func setEnvironmentCloudAliasViaMCP(ctx context.Context, endpoint, bearer, tenant, environment, alias string) (eruncommon.EnvConfig, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
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

// runPodRawFromMCP runs an argv inside the runtime pod via the per-env MCP
// endpoint's raw tool and returns its stdout. The idle-probe header keeps
// these diagnostic reads from registering as activity, so a hover or an
// open Diagnostics console never holds an idle env awake.
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

// loadPodBranchFromMCP reads the env worktree's current git branch from
// inside the runtime pod via the per-env MCP endpoint's raw tool — the
// sidebar hover card's "Working on" source for remote envs.
func loadPodBranchFromMCP(ctx context.Context, endpoint, bearer string) (string, error) {
	out, err := runPodRawFromMCP(ctx, endpoint, bearer, []string{"git", "rev-parse", "--abbrev-ref", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func loadIdleStatusFromMCP(ctx context.Context, endpoint, bearer string) (eruncommon.EnvironmentIdleStatus, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
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
			"command": []string{"sh", "-lc", "namespace=${ERUN_NAMESPACE:-}; if [ -z \"$namespace\" ] && [ -r /var/run/secrets/kubernetes.io/serviceaccount/namespace ]; then namespace=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace); fi; kubectl --context \"${ERUN_KUBERNETES_CONTEXT:-in-cluster}\" --namespace \"$namespace\" logs \"deployment/${ERUN_TENANT:-erun}-devops\" -c erun-backend-api --tail 400"},
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
