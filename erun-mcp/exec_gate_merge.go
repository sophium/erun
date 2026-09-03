package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecGateMergeSource is one branch to squash into the prospective merge, in
// the order it should land, carrying its own commit message.
type ExecGateMergeSource struct {
	Branch  string `json:"branch" jsonschema:"branch to fetch and squash-merge in"`
	Message string `json:"message" jsonschema:"this branch's own squash commit message — its review's name, since that commit is what ends up on targetBranch if the gate passes"`
}

// ExecGateMergeInput builds the prospective merge a merge queue promotion
// (or batch) gates: fetch targetBranch and every source, check out a fresh
// local branch named targetBranch at its own current remote tip, and
// squash-merge each source onto it in turn, each as its own commit.
type ExecGateMergeInput struct {
	Sources      []ExecGateMergeSource `json:"sources" jsonschema:"branches to squash-merge in, in order; more than one batches several unmerged branches into one prospective merge so the gate that follows tests whether they compile together"`
	TargetBranch string                `json:"targetBranch" jsonschema:"branch the squash merge(s) land onto, checked out fresh from its own current remote tip"`
	Remote       string                `json:"remote,omitempty" jsonschema:"git remote to fetch and merge from; defaults to origin"`
	UnderLeaseID string                `json:"underLeaseId,omitempty" jsonschema:"id of an exclusive environment claim this caller already holds. This rewrites the environment's one shared worktree, so it is refused while anything else holds the environment exclusively -- a drive that took the claim for its own whole window names it here so its own hold does not refuse it"`
	Preview      bool                  `json:"preview,omitempty" jsonschema:"when true, trace the fetch, checkout, and each squash merge and commit without running them"`
	Verbosity    int                   `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execGateMergeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecGateMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecGateMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
		var merged *eruncommon.GateMergeWorkingTreeResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx eruncommon.Context, workDir string) error {
			sources := make([]eruncommon.GateMergeSource, len(input.Sources))
			for i, source := range input.Sources {
				sources[i] = eruncommon.GateMergeSource{Branch: source.Branch, Message: source.Message}
			}
			result, err := eruncommon.GateMergeWorkingTree(runCtx, workDir, eruncommon.GateMergeWorkingTreeParams{
				Sources:      sources,
				TargetBranch: input.TargetBranch,
				Remote:       input.Remote,
				UnderLeaseID: input.UnderLeaseID,
			}, eruncommon.GateMergeWorkingTreeDependencies{})
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
