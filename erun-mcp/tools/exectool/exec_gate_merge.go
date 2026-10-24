package exectool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// ExecGateMergeSource is one branch to squash, in landing order.
type ExecGateMergeSource struct {
	Branch  string `json:"branch" jsonschema:"branch to fetch and squash-merge in"`
	Message string `json:"message" jsonschema:"this branch's own squash commit message — its review's name, since that commit is what ends up on targetBranch if its build succeeds. The branch's own load-bearing trailers (Closes #N, Reproduces:, Regression-Test:) are appended beneath it automatically, and a trailer the message already carries is not repeated"`
}

// ExecGateMergeInput squash-merges each source, in order, onto a fresh branch
// at targetBranch's remote tip.
type ExecGateMergeInput struct {
	Sources      []ExecGateMergeSource `json:"sources" jsonschema:"branches to squash-merge in, in order; more than one batches several unmerged branches into one prospective merge so the build that follows tests whether they compile together"`
	TargetBranch string                `json:"targetBranch" jsonschema:"branch the squash merge(s) land onto, checked out fresh from its own current remote tip"`
	Remote       string                `json:"remote,omitempty" jsonschema:"git remote to fetch and merge from; defaults to origin"`
	UnderLeaseID string                `json:"underLeaseId,omitempty" jsonschema:"id of an exclusive environment claim this caller already holds. This rewrites the environment's one shared worktree, so it is refused while anything else holds the environment exclusively -- a drive that took the claim for its own whole window names it here so its own hold does not refuse it"`
	Preview      bool                  `json:"preview,omitempty" jsonschema:"when true, trace the fetch, checkout, and each squash merge and commit without running them"`
	Verbosity    int                   `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func ExecGateMergeTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecGateMergeInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecGateMergeInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
		var merged *mergequeue.GateMergeWorkingTreeResult
		output, err := shared.RunRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx core.Context, workDir string) error {
			sources := make([]mergequeue.GateMergeSource, len(input.Sources))
			for i, source := range input.Sources {
				sources[i] = mergequeue.GateMergeSource{Branch: source.Branch, Message: source.Message}
			}
			result, err := mergequeue.GateMergeWorkingTree(runCtx, workDir, mergequeue.GateMergeWorkingTreeParams{
				Sources:      sources,
				TargetBranch: input.TargetBranch,
				Remote:       input.Remote,
				UnderLeaseID: input.UnderLeaseID,
			}, mergequeue.GateMergeWorkingTreeDependencies{})
			if err != nil {
				return err
			}
			merged = &result
			return nil
		})
		if err == nil && merged != nil {
			output.GateMerge = merged
		}
		return nil, output, err
	}
}
