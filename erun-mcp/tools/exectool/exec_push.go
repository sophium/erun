package exectool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// ExecPushInput takes the branch as an explicit claim to verify.
type ExecPushInput struct {
	Branch    string `json:"branch" jsonschema:"branch the caller believes the working tree is on; verified against the actual current branch and refused, loudly, on mismatch"`
	Remote    string `json:"remote,omitempty" jsonschema:"git remote to push to; defaults to origin"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, verify the branch and trace the push without running it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func ExecPushTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecPushInput) (*mcp.CallToolResult, toolio.CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecPushInput) (*mcp.CallToolResult, toolio.CommandOutput, error) {
		var pushed *model.PushWorkingTreeBranchResult
		output, err := runcmd.RunRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx core.Context, workDir string) error {
			result, err := gitx.PushWorkingTreeBranch(runCtx, workDir, model.PushWorkingTreeBranchParams{
				Branch: input.Branch,
				Remote: input.Remote,
			}, gitx.PushWorkingTreeBranchDependencies{})
			if err != nil {
				return err
			}
			pushed = &result
			return nil
		})
		if err == nil && pushed != nil {
			output.Push = pushed
		}
		return nil, output, err
	}
}
