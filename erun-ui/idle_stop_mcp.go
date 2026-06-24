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

// idleStopMCPTimeout caps every MCP-tool round-trip the desktop
// makes for the idle-stop tools. Without a deadline,
// `context.Background()` lets `Connect` and `CallTool` hang for the
// app's entire lifetime if the runtime image is older than the
// desktop (the tool isn't registered, the SDK's error surface
// hasn't bubbled up yet) or if the kubectl port-forward is wedged.
// 10 s is long enough to absorb a slow cold-start handshake on a
// real cluster and short enough that the "Loading…" placeholder
// resolves into a real error message during a normal user
// interaction.
const idleStopMCPTimeout = 10 * time.Second

// cancelStopPendingViaMCP calls the in-pod `idle_stop_cancel` tool
// to dismiss the grace-period warning for the env behind `endpoint`.
// The in-pod handler removes <home>/.erun/<tenant>/<env>/stop-pending.json;
// the next idle poll from any client (including the in-pod monitor's
// next 30 s tick) re-evaluates eligibility and re-arms the warning
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

// loadStopHistoryViaMCP calls the in-pod `idle_stop_history` tool
// and returns the entries verbatim. The in-pod handler reads
// stop-history.json from the env's shared PVC, so a desktop running
// after a long break sees the same audit trail the in-pod monitor
// has been appending to.
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

// recordManualStopViaMCP calls the in-pod `idle_stop_record` tool
// to append a host-manual entry to stop-history.json. Used by the
// desktop's Stop button so the History tab also explains "you
// clicked Stop", not just auto-stops fired by the in-pod monitor.
// Reason is the free-form text rendered on the row; passing the
// empty string defers to the tool's default ("Manual stop"). On
// older runtime images that do not register the tool yet, the
// caller swallows the formatted error so manual stops still
// succeed — formatIdleStopMCPError points at the rebuild fix.
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

// formatIdleStopMCPError rewrites the SDK error to spell out the
// most common failure mode plainly: the runtime image in the pod
// predates this desktop version and does not have the new tool
// registered yet. The SDK returns "Method not found" / "tool not
// found" or wraps the JSON-RPC code -32601; we match those shapes
// and translate to a single user-facing line that points at the
// fix (rebuild the runtime image). For any other error we surface
// the raw message so cluster-side failures (port-forward dead,
// MCP container OOMed, etc.) still reach the user.
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
