package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ListInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func listTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ListInput) (*mcp.CallToolResult, eruncommon.ListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, eruncommon.ListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(false, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.TraceCommand("", "erun", "list")

		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, eruncommon.ListResult{}, err
		}

		result, err := eruncommon.ResolveListResult(runtime.Store, func() (string, string, error) {
			return runtimeFindProjectRoot(runtime.Context, workDir)
		}, runtimeOpenParams(runtime.Context))
		if err != nil {
			return nil, eruncommon.ListResult{}, err
		}

		return nil, result, nil
	}
}

func runtimeOpenParams(runtime RuntimeContext) eruncommon.OpenParams {
	tenant := strings.TrimSpace(runtime.Tenant)
	environment := strings.TrimSpace(runtime.Environment)

	switch {
	case tenant != "" && environment != "":
		return eruncommon.OpenParams{Tenant: tenant, Environment: environment}
	case tenant != "":
		return eruncommon.OpenParams{Tenant: tenant, UseDefaultEnvironment: true}
	case environment != "":
		return eruncommon.OpenParams{Environment: environment, UseDefaultTenant: true}
	default:
		return eruncommon.OpenParams{UseDefaultTenant: true, UseDefaultEnvironment: true}
	}
}
