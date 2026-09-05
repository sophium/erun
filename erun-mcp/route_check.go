package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// route_check.go mirrors gate_runs.go's platform-call shape for
// exec_route-check: proving every route erun-backend-api's router registers
// is actually reachable on a deployed plane, rather than trusting that
// "merged" means "deployed".

// RouteCheckToolResult is the route-check tool's result.
type RouteCheckToolResult struct {
	Preview bool                        `json:"preview"`
	Result  eruncommon.RouteCheckResult `json:"result,omitempty"`
	Trace   []string                    `json:"trace,omitempty"`
}

type ExecRouteCheckInput struct {
	platformAliasInput
	RoutesDir string `json:"routesDir,omitempty" jsonschema:"path to erun-backend-api/internal/routes; defaults to that path under the runtime repo's project root"`
}

func execRouteCheckTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecRouteCheckInput) (*mcp.CallToolResult, RouteCheckToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecRouteCheckInput) (*mcp.CallToolResult, RouteCheckToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_route-check"
		result, err := eruncommon.RunRouteCheck(ctx, runtime.Store, input.Alias, eruncommon.RouteCheckParams{
			RoutesDir: input.RoutesDir,
		}, cloudDependencies())
		if err != nil {
			return nil, RouteCheckToolResult{}, err
		}
		return nil, RouteCheckToolResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
