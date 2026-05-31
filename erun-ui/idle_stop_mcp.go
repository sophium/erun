package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// cancelStopPendingViaMCP calls the in-pod `idle_stop_cancel` tool
// to dismiss the grace-period warning for the env behind `endpoint`.
// The in-pod handler removes <home>/.erun/<tenant>/<env>/stop-pending.json;
// the next idle poll from any client (including the in-pod monitor's
// next 30 s tick) re-evaluates eligibility and re-arms the warning
// if the env is still idle.
func cancelStopPendingViaMCP(ctx context.Context, endpoint, tenant, environment string) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: idleProbeRoundTripper{},
		},
		DisableStandaloneSSE: true,
	}, nil)
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
		return fmt.Errorf("idle_stop_cancel: %w", err)
	}
	return nil
}

// loadStopHistoryViaMCP calls the in-pod `idle_stop_history` tool
// and returns the entries verbatim. The in-pod handler reads
// stop-history.json from the env's shared PVC, so a desktop running
// after a long break sees the same audit trail the in-pod monitor
// has been appending to.
func loadStopHistoryViaMCP(ctx context.Context, endpoint, tenant, environment string) ([]eruncommon.EnvironmentStopHistoryEntry, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: idleProbeRoundTripper{},
		},
		DisableStandaloneSSE: true,
	}, nil)
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
		return nil, fmt.Errorf("idle_stop_history: %w", err)
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
