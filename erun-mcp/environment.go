package erunmcp

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type EnvironmentInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be inspected; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to inspect; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, skip the live doctor deploy diagnosis (the only part of this call that runs helm/kubectl) and report health as not observed rather than run it"`
}

// environmentTool is the environment read model's one aggregate call: an
// environment's list-style summary plus its resolved lifecycle state
// (running/idle/deploy-failed/stopped/unknown), its idle policy + activity
// snapshot, its cloud-context config, and a doctor deploy diagnosis -- each
// reusing the existing list/idle/doctor resolvers rather than
// re-implementing them. Cloud-context power state is never refreshed live
// here (that needs the operator's own AWS credentials, which do not reach
// inside this pod), so a managed-cloud environment reports state "unknown"
// rather than guessing from a status this package never persists.
func environmentTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, EnvironmentInput) (*mcp.CallToolResult, eruncommon.EnvironmentReadModel, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input EnvironmentInput) (*mcp.CallToolResult, eruncommon.EnvironmentReadModel, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, eruncommon.EnvironmentReadModel{}, err
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, 0, nil, &traceOutput, &traceOutput)
		model, err := eruncommon.ResolveEnvironmentReadModel(ctx, runtime.Store, tenant, environment, time.Now())
		if err != nil {
			return nil, eruncommon.EnvironmentReadModel{}, err
		}
		return nil, model, nil
	}
}
