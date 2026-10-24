package idle

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

type IdleInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be inspected; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to inspect; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func IdleTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleInput) (*mcp.CallToolResult, model.EnvironmentIdleStatus, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleInput) (*mcp.CallToolResult, model.EnvironmentIdleStatus, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, model.EnvironmentIdleStatus{}, err
		}
		status, err := activity.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, time.Now())
		if err != nil {
			return nil, model.EnvironmentIdleStatus{}, err
		}
		return nil, status, nil
	}
}
