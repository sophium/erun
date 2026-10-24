package services

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type ServicesInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be listed; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to list; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl calls that would run without executing them"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// ServicesTool lists Services with their ports and any erun-expose public
// address. It only runs `kubectl get`, so it needs erun:read, not erun:admin.
func ServicesTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ServicesInput) (*mcp.CallToolResult, model.EnvironmentServiceList, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ServicesInput) (*mcp.CallToolResult, model.EnvironmentServiceList, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, model.EnvironmentServiceList{}, err
		}
		target, err := resolve.ResolveOpen(runtime.Store, model.OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return nil, model.EnvironmentServiceList{}, err
		}
		runCtx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		result, err := expose.RunListEnvironmentServices(runCtx, cloud.ShellLaunchParamsFromResult(target))
		if err != nil {
			return nil, model.EnvironmentServiceList{}, err
		}
		return nil, result, nil
	}
}
