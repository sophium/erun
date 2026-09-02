package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecPushInput takes the branch as an explicit claim to verify, mirroring
// `erun exec commit`'s own CommitInput. Named distinctly from build.go's
// PushInput (the container-image `push` tool), which this is unrelated to.
type ExecPushInput struct {
	Branch    string `json:"branch" jsonschema:"branch the caller believes the working tree is on; verified against the actual current branch and refused, loudly, on mismatch"`
	Remote    string `json:"remote,omitempty" jsonschema:"git remote to push to; defaults to origin"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, verify the branch and trace the push without running it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execPushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecPushInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecPushInput) (*mcp.CallToolResult, CommandOutput, error) {
		var pushed *eruncommon.PushWorkingTreeBranchResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx eruncommon.Context, workDir string) error {
			result, err := eruncommon.PushWorkingTreeBranch(runCtx, workDir, eruncommon.PushWorkingTreeBranchParams{
				Branch: input.Branch,
				Remote: input.Remote,
			}, eruncommon.PushWorkingTreeBranchDependencies{})
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
