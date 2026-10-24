package exectool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type ExecMergeInput struct {
	TargetBranch string `json:"targetBranch" jsonschema:"branch to fetch and merge in; the merge always produces a merge commit, never a rebase, so review comments anchored to a commit id are never orphaned"`
	Remote       string `json:"remote,omitempty" jsonschema:"git remote to fetch and merge from; defaults to origin"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, trace the fetch and merge without running them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func ExecMergeTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecMergeInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecMergeInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
		var merged *model.MergeWorkingTreeBranchResult
		output, err := shared.RunRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx core.Context, workDir string) error {
			result, err := mergequeue.MergeWorkingTreeBranch(runCtx, workDir, model.MergeWorkingTreeBranchParams{
				TargetBranch: input.TargetBranch,
				Remote:       input.Remote,
			}, mergequeue.MergeWorkingTreeBranchDependencies{})
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
