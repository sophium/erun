package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// IdleStopCancelInput targets the env whose stop-pending.json should
// be cleared. Defaults to the runtime's tenant/environment context
// when the caller does not specify, matching every other tool's
// behavior.
type IdleStopCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have its pending stop cancelled; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to cancel; defaults to the server environment context"`
}

// IdleStopCancelResult is intentionally tiny — the desktop fires
// this through MCP and then re-fetches idle status to pick up the
// cleared pending state. Cleared is true on both the "was armed,
// now cleared" and "wasn't armed, no-op" branches; the in-pod
// monitor's next stop-ready tick decides whether to re-arm.
type IdleStopCancelResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Cleared     bool   `json:"cleared"`
}

func idleStopCancelTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
			return nil, IdleStopCancelResult{}, fmt.Errorf("tenant and environment are required")
		}
		if err := eruncommon.ClearEnvironmentStopPending(tenant, environment); err != nil {
			return nil, IdleStopCancelResult{}, err
		}
		return nil, IdleStopCancelResult{
			Tenant:      tenant,
			Environment: environment,
			Cleared:     true,
		}, nil
	}
}

// IdleStopHistoryInput requests the last N auto-stop entries for
// an env. Defaults to the runtime's tenant/environment context.
type IdleStopHistoryInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be queried; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to query; defaults to the server environment context"`
}

// IdleStopHistoryResult wraps the array so the JSON-Schema surface
// describes a stable object shape. Entries are newest-first, capped
// at common.StopHistoryCap.
type IdleStopHistoryResult struct {
	Tenant      string                                    `json:"tenant"`
	Environment string                                    `json:"environment"`
	Entries     []eruncommon.EnvironmentStopHistoryEntry `json:"entries"`
}

func idleStopHistoryTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
			return nil, IdleStopHistoryResult{}, fmt.Errorf("tenant and environment are required")
		}
		entries, err := eruncommon.LoadEnvironmentStopHistory(tenant, environment)
		if err != nil {
			return nil, IdleStopHistoryResult{}, err
		}
		return nil, IdleStopHistoryResult{
			Tenant:      tenant,
			Environment: environment,
			Entries:     entries,
		}, nil
	}
}
