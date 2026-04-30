package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DiffInput struct {
	Verbosity      int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Scope          string `json:"scope,omitempty" jsonschema:"diff scope: current, commit, or all"`
	SelectedCommit string `json:"selectedCommit,omitempty" jsonschema:"oldest commit hash to include when scope is commit"`
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
		result, err := eruncommon.ResolveGitDiffWithOptions(workDir, eruncommon.DiffOptions{
			Scope:          input.Scope,
			SelectedCommit: input.SelectedCommit,
		}, eruncommon.GitCommandRunner)
		return nil, result, err
	}
}
