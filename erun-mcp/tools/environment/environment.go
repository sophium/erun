package environment

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type EnvironmentInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be inspected; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to inspect; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, skip the live doctor deploy diagnosis (the only part of this call that runs helm/kubectl) and report health as not observed rather than run it"`
}

// EnvironmentTool aggregates summary, lifecycle state, idle status, cloud
// context and deploy diagnosis. Cloud power state cannot be refreshed in-pod,
// so managed-cloud environments report "unknown".
func EnvironmentTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, EnvironmentInput) (*mcp.CallToolResult, lifecycle.EnvironmentReadModel, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input EnvironmentInput) (*mcp.CallToolResult, lifecycle.EnvironmentReadModel, error) {
		tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, lifecycle.EnvironmentReadModel{}, err
		}
		traceOutput := strings.Builder{}
		ctx := shared.RuntimeCallContext(input.Preview, 0, nil, &traceOutput, &traceOutput)
		model, err := lifecycle.ResolveEnvironmentReadModel(ctx, runtime.Store, tenant, environment, time.Now())
		if err != nil {
			return nil, lifecycle.EnvironmentReadModel{}, err
		}
		return nil, model, nil
	}
}
