package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
)

// idleStopMCPTimeout keeps a stale image or wedged port-forward from hanging;
// 10s absorbs a cold-start handshake.
const idleStopMCPTimeout = 10 * time.Second

// The next idle poll re-arms the warning if the env is still idle.
func (s *Service) cancelStopPendingViaMCP(ctx context.Context, endpoint, bearer, tenant, environment string) error {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: s.d.ClientVersion()}, nil)
	session, err := client.Connect(ctx, s.d.MCPTransport(endpoint, bearer), nil)
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

func (s *Service) loadStopHistoryViaMCP(ctx context.Context, endpoint, bearer, tenant, environment string) ([]model.EnvironmentStopHistoryEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: s.d.ClientVersion()}, nil)
	session, err := client.Connect(ctx, s.d.MCPTransport(endpoint, bearer), nil)
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
		Entries []model.EnvironmentStopHistoryEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Entries == nil {
		return []model.EnvironmentStopHistoryEntry{}, nil
	}
	return payload.Entries, nil
}

func (s *Service) recordManualStopViaMCP(ctx context.Context, endpoint, bearer, tenant, environment, reason, cloudContextName string) error {
	ctx, cancel := context.WithTimeout(ctx, idleStopMCPTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: s.d.ClientVersion()}, nil)
	session, err := client.Connect(ctx, s.d.MCPTransport(endpoint, bearer), nil)
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

// formatIdleStopMCPError points a missing idle_stop_* tool at a rebuild; other
// errors pass through raw.
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
