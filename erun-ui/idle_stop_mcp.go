package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// idleStopMCPTimeout bounds every idle-stop MCP round-trip so a stale
// runtime image or a wedged port-forward surfaces as an error instead
// of hanging for the app's lifetime; 10 s absorbs a cold-start
// handshake while still resolving the loading state promptly.
const idleStopMCPTimeout = 10 * time.Second

// cancelStopPendingViaMCP dismisses the grace-period warning for an env.
// The dismissal is not permanent: the next idle poll re-arms the warning
// if the env is still idle.
func cancelStopPendingViaMCP(ctx context.Context, endpoint, bearer, tenant, environment string) error {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()
	args := map[string]any{}
	if tenant != "" {
		args["tenant"] = tenant
	}
	if environment != "" {
		args["environment"] = environment
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "idle_stop_cancel",
		Arguments: args,
	}); err != nil {
		return formatIdleStopMCPError("idle_stop_cancel", err)
	}
	return nil
}

// loadStopHistoryViaMCP reads the stop-history audit trail from the pod,
// the source of truth the in-pod monitor appends to, so a desktop
// returning after a break sees the complete history.
func loadStopHistoryViaMCP(ctx context.Context, endpoint, bearer, tenant, environment string) ([]eruncommon.EnvironmentStopHistoryEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = session.Close()
	}()
	args := map[string]any{}
	if tenant != "" {
		args["tenant"] = tenant
	}
	if environment != "" {
		args["environment"] = environment
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "idle_stop_history",
		Arguments: args,
	})
	if err != nil {
		return nil, formatIdleStopMCPError("idle_stop_history", err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Entries []eruncommon.EnvironmentStopHistoryEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Entries == nil {
		return []eruncommon.EnvironmentStopHistoryEntry{}, nil
	}
	return payload.Entries, nil
}

// recordManualStopViaMCP records a manual stop in the audit trail so the
// History tab distinguishes "you clicked Stop" from auto-stops fired by
// the in-pod monitor.
func recordManualStopViaMCP(ctx context.Context, endpoint, bearer, tenant, environment, reason, cloudContextName string) error {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()
	args := map[string]any{}
	if tenant != "" {
		args["tenant"] = tenant
	}
	if environment != "" {
		args["environment"] = environment
	}
	if strings.TrimSpace(reason) != "" {
		args["reason"] = reason
	}
	if strings.TrimSpace(cloudContextName) != "" {
		args["cloudContextName"] = cloudContextName
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "idle_stop_record",
		Arguments: args,
	}); err != nil {
		return formatIdleStopMCPError("idle_stop_record", err)
	}
	return nil
}

// formatIdleStopMCPError translates the common "runtime image predates
// this desktop and lacks the idle_stop_* tool" failure into a single
// user-facing line pointing at the rebuild fix; any other error passes
// through raw so real cluster-side failures still reach the user.
func formatIdleStopMCPError(tool string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "method not found"),
		strings.Contains(lower, "unknown method"),
		strings.Contains(lower, "tool not found"),
		strings.Contains(lower, "-32601"),
		strings.Contains(lower, "no such tool"):
		return fmt.Errorf(
			"%s: this runtime image does not yet support the auto-stop history feature. "+
				"Rebuild and redeploy the env (erun deploy <tenant> <env>) so the pod runs "+
				"a runtime image that includes the idle_stop_* MCP tools",
			tool,
		)
	}
	return fmt.Errorf("%s: %s", tool, msg)
}
