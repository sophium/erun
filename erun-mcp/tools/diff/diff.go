package diff

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/diff"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type DiffInput struct {
	Verbosity      int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Scope          string `json:"scope,omitempty" jsonschema:"diff scope: current, commit, or all"`
	SelectedCommit string `json:"selectedCommit,omitempty" jsonschema:"oldest commit hash to include when scope is commit"`
	Target         string `json:"target,omitempty" jsonschema:"which repository to diff: 'env' (default) for the environment's runtime repo, or 'erun' for the contribute-mode clone at $HOME/git/erun"`
}

func DiffTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DiffInput) (*mcp.CallToolResult, model.DiffResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, model.DiffResult, error) {
		envRoot, err := toolio.RuntimeRepoPath(runtime.Context)
		if err != nil {
			return nil, model.DiffResult{}, err
		}
		workDir, err := diff.ResolveDiffTargetRoot(input.Target, envRoot, "")
		if err != nil {
			return nil, model.DiffResult{}, err
		}
		if workDir == "" {
			return nil, model.DiffResult{}, fmt.Errorf("diff target %q resolved to an empty working directory", input.Target)
		}
		traceOutput := new(strings.Builder)
		ctx := toolio.RuntimeCallContext(false, input.Verbosity, nil, traceOutput, traceOutput)
		ctx.TraceCommand(workDir, "git", "diff", "--no-color", "--no-ext-diff")
		result, err := diff.ResolveGitDiffWithOptions(workDir, model.DiffOptions{
			Scope:          input.Scope,
			SelectedCommit: input.SelectedCommit,
			Target:         diff.NormalizeDiffTarget(input.Target),
		}, gitx.GitCommandRunner)
		return nil, result, err
	}
}
