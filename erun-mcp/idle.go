package erunmcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type IdleInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be inspected; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to inspect; defaults to the server environment context"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func idleTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleInput) (*mcp.CallToolResult, eruncommon.EnvironmentIdleStatus, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleInput) (*mcp.CallToolResult, eruncommon.EnvironmentIdleStatus, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		status, err := eruncommon.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, time.Now())
		if err != nil {
			return nil, eruncommon.EnvironmentIdleStatus{}, err
		}
		return nil, status, nil
	}
}
