package expose

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/expose"
	"github.com/sophium/erun/erun-mcp/tools/shared/clouddeps"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// RouteCheckToolResult is the route-check tool's result.
type RouteCheckToolResult struct {
	Preview bool                   `json:"preview"`
	Result  model.RouteCheckResult `json:"result,omitempty"`
	Trace   []string               `json:"trace,omitempty"`
}

type ExecRouteCheckInput struct {
	toolio.PlatformAliasInput
	RoutesDir string `json:"routesDir,omitempty" jsonschema:"path to erun-backend-api/internal/routes; defaults to that path under the runtime repo's project root"`
}

func ExecRouteCheckTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecRouteCheckInput) (*mcp.CallToolResult, RouteCheckToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecRouteCheckInput) (*mcp.CallToolResult, RouteCheckToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_route-check"
		result, err := expose.RunRouteCheck(ctx, runtime.Store, input.Alias, model.RouteCheckParams{
			RoutesDir: input.RoutesDir,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, RouteCheckToolResult{}, err
		}
		return nil, RouteCheckToolResult{Preview: input.Preview, Result: result, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
