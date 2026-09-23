package erunmcp

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ServicesInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be listed; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to list; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl calls that would run without executing them"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// servicesTool is read-only by construction: RunListEnvironmentServices issues
// only `kubectl get`, so this tool carries the erun:read capability rather than
// erun:admin (see mcpReadOnlyTools), unlike expose itself.
//
// It exists because expose takes a service name and, until this tool, the MCP
// surface held nothing that could report one: the name is a derived label
// resolving to the in-namespace Service <tenant>-<service>, so an agent could
// act without being able to orient. Each Service comes back with its ports and,
// when an erun-expose Ingress already fronts it, the public address it has --
// the same read the desktop's Ports tab renders as its Service picker.
func servicesTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ServicesInput) (*mcp.CallToolResult, eruncommon.EnvironmentServiceList, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ServicesInput) (*mcp.CallToolResult, eruncommon.EnvironmentServiceList, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, eruncommon.EnvironmentServiceList{}, err
		}
		target, err := eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: tenant, Environment: environment})
		if err != nil {
			return nil, eruncommon.EnvironmentServiceList{}, err
		}
		runCtx := runtimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		result, err := eruncommon.RunListEnvironmentServices(runCtx, eruncommon.ShellLaunchParamsFromResult(target))
		if err != nil {
			return nil, eruncommon.EnvironmentServiceList{}, err
		}
		return nil, result, nil
	}
}
