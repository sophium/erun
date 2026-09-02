package erunmcp

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ServicesInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment's Services should be listed; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment whose Services should be listed; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl calls that would run without executing them"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// ServicesOutput wraps the listing in an object: the MCP SDK requires a tool's
// output schema to be a JSON object, so a bare array cannot be returned directly.
type ServicesOutput struct {
	Services []eruncommon.EnvironmentService `json:"services"`
}

// servicesTool is read-only by construction, same as observeTool: two
// `kubectl get` calls, never a mutation.
func servicesTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ServicesInput) (*mcp.CallToolResult, ServicesOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ServicesInput) (*mcp.CallToolResult, ServicesOutput, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ServicesOutput{}, err
		}
		target, err := eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return nil, ServicesOutput{}, err
		}
		req := eruncommon.ShellLaunchParamsFromResult(target)
		runCtx := runtimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		services, err := eruncommon.ListEnvironmentServices(runCtx, req, tenant)
		if err != nil {
			return nil, ServicesOutput{}, err
		}
		return nil, ServicesOutput{Services: services}, nil
	}
}
