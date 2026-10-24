package exectool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// ExecClosePRInput closes branch's PR after review_report-merged; GitHub never
// reconciles gate-merge's squash commit with the PR itself.
type ExecClosePRInput struct {
	Branch        string `json:"branch" jsonschema:"source branch whose open pull request should close — the review's sourceBranch"`
	TargetBranch  string `json:"targetBranch" jsonschema:"the pull request's base branch — the review's targetBranch"`
	RemoteURL     string `json:"remoteUrl" jsonschema:"the github.com remote the pull request lives on"`
	GatedCommit   string `json:"gatedCommit" jsonschema:"branch's tip when the merge queue actually fetched and built it; closing is refused if the pull request's current head does not match"`
	LandingCommit string `json:"landingCommit" jsonschema:"the commit that actually landed on targetBranch, recorded in a comment on the pull request"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"when true, trace the lookup without closing or commenting on anything"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func ExecClosePRTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecClosePRInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecClosePRInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
		var closed *review.ClosePullRequestResult
		output, err := shared.RunRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx core.Context, _ string) error {
			result, err := review.ClosePullRequest(runCtx, review.ClosePullRequestParams{
				RemoteURL:     input.RemoteURL,
				Branch:        input.Branch,
				TargetBranch:  input.TargetBranch,
				GatedCommit:   input.GatedCommit,
				LandingCommit: input.LandingCommit,
			}, review.ClosePullRequestDependencies{})
			if err != nil {
				return err
			}
			closed = &result
			return nil
		})
		if err == nil && closed != nil {
			output.ClosePullRequest = closed
		}
		return nil, output, err
	}
}
