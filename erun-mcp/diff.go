package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DiffInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func diffTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DiffInput) (*mcp.CallToolResult, eruncommon.DiffResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, eruncommon.DiffResult, error) {
		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, eruncommon.DiffResult{}, err
		}
		traceOutput := new(strings.Builder)
		ctx := runtimeCallContext(false, input.Verbosity, nil, traceOutput, traceOutput)
		ctx.TraceCommand(workDir, "git", "diff", "--no-color", "--no-ext-diff")
		result, err := eruncommon.ResolveGitDiff(workDir, eruncommon.GitCommandRunner)
		return nil, result, err
	}
}
