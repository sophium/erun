package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecGateMergeInput builds the prospective squash merge a merge queue
// promotion gates: fetch sourceBranch and targetBranch, check out a fresh
// local branch named targetBranch at its own current remote tip, and
// squash-merge sourceBranch onto it as one commit carrying message.
type ExecGateMergeInput struct {
	SourceBranch string `json:"sourceBranch" jsonschema:"branch to fetch and squash-merge in"`
	TargetBranch string `json:"targetBranch" jsonschema:"branch the squash merge lands onto, checked out fresh from its own current remote tip"`
	Message      string `json:"message" jsonschema:"squash commit message — the review's name, since that commit is what ends up on targetBranch if the gate passes"`
	Remote       string `json:"remote,omitempty" jsonschema:"git remote to fetch and merge from; defaults to origin"`
	Preview      bool   `json:"preview,omitempty" jsonschema:"when true, trace the fetch, checkout, squash merge, and commit without running them"`
	Verbosity    int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execGateMergeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecGateMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecGateMergeInput) (*mcp.CallToolResult, CommandOutput, error) {
		var merged *eruncommon.GateMergeWorkingTreeResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			result, err := eruncommon.GateMergeWorkingTree(runCtx, workDir, eruncommon.GateMergeWorkingTreeParams{
				SourceBranch: input.SourceBranch,
				TargetBranch: input.TargetBranch,
				Message:      input.Message,
				Remote:       input.Remote,
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
