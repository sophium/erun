package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecClosePRInput closes branch's open pull request on GitHub, once `erun
// review_report-merged` has already succeeded, and records landingCommit on
// it — gate-merge's squash commit is never the branch head GitHub tracks, so
// GitHub never reconciles a queued merge with its pull request on its own.
type ExecClosePRInput struct {
	Branch        string `json:"branch" jsonschema:"source branch whose open pull request should close — the review's sourceBranch"`
	TargetBranch  string `json:"targetBranch" jsonschema:"the pull request's base branch — the review's targetBranch"`
	RemoteURL     string `json:"remoteUrl" jsonschema:"the github.com remote the pull request lives on"`
	GatedCommit   string `json:"gatedCommit" jsonschema:"branch's tip when the gate actually fetched and tested it; closing is refused if the pull request's current head does not match"`
	LandingCommit string `json:"landingCommit" jsonschema:"the commit that actually landed on targetBranch, recorded in a comment on the pull request"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"when true, trace the lookup without closing or commenting on anything"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execClosePRTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecClosePRInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecClosePRInput) (*mcp.CallToolResult, CommandOutput, error) {
		var closed *eruncommon.ClosePullRequestResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			result, err := eruncommon.ClosePullRequest(runCtx, eruncommon.ClosePullRequestParams{
				RemoteURL:     input.RemoteURL,
				Branch:        input.Branch,
				TargetBranch:  input.TargetBranch,
				GatedCommit:   input.GatedCommit,
				LandingCommit: input.LandingCommit,
			}, eruncommon.ClosePullRequestDependencies{})
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
