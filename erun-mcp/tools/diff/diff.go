package diff

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type DiffInput struct {
	Verbosity      int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Scope          string `json:"scope,omitempty" jsonschema:"diff scope: current, commit, or all"`
	SelectedCommit string `json:"selectedCommit,omitempty" jsonschema:"oldest commit hash to include when scope is commit"`
	Target         string `json:"target,omitempty" jsonschema:"which repository to diff: 'env' (default) for the environment's runtime repo, or 'erun' for the contribute-mode clone at $HOME/git/erun"`
}

func DiffTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DiffInput) (*mcp.CallToolResult, lifecycle.DiffResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, lifecycle.DiffResult, error) {
		envRoot, err := shared.RuntimeRepoPath(runtime.Context)
		if err != nil {
			return nil, lifecycle.DiffResult{}, err
		}
		workDir, err := lifecycle.ResolveDiffTargetRoot(input.Target, envRoot, "")
		if err != nil {
			return nil, lifecycle.DiffResult{}, err
		}
		if workDir == "" {
			return nil, lifecycle.DiffResult{}, fmt.Errorf("diff target %q resolved to an empty working directory", input.Target)
		}
		traceOutput := new(strings.Builder)
		ctx := shared.RuntimeCallContext(false, input.Verbosity, nil, traceOutput, traceOutput)
		ctx.TraceCommand(workDir, "git", "diff", "--no-color", "--no-ext-diff")
		result, err := lifecycle.ResolveGitDiffWithOptions(workDir, lifecycle.DiffOptions{
			Scope:          input.Scope,
			SelectedCommit: input.SelectedCommit,
			Target:         lifecycle.NormalizeDiffTarget(input.Target),
		}, gitx.GitCommandRunner)
		return nil, result, err
	}
}
