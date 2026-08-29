package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecMergeInput names the branch to bring into the runtime repo's current
// branch, mirroring ExecPushInput's shape.
type ExecMergeInput struct {
	TargetBranch string `json:"targetBranch" jsonschema:"branch to fetch and merge in; the merge always produces a merge commit, never a rebase, so review comments anchored to a commit id are never orphaned"`
	Remote       string `json:"remote,omitempty" jsonschema:"git remote to fetch and merge from; defaults to origin"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, trace the fetch and merge without running them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execMergeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
		var merged *eruncommon.MergeWorkingTreeBranchResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			result, err := eruncommon.MergeWorkingTreeBranch(runCtx, workDir, eruncommon.MergeWorkingTreeBranchParams{
				TargetBranch: input.TargetBranch,
				Remote:       input.Remote,
			}, eruncommon.MergeWorkingTreeBranchDependencies{})
			if err != nil {
				return err
			}
			merged = &result
			return nil
		})
		if err == nil && merged != nil {
			output.Merge = merged
		}
		return nil, output, err
	}
}
