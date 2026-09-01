package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecReportCommitStatusInput reports a GitHub commit status for a merge
// queue gate result — the last step that turns a build's outcome into
// something a required status check on the remote's branch protection can
// point at.
type ExecReportCommitStatusInput struct {
	Commit      string `json:"commit" jsonschema:"full commit SHA the status attaches to — the review's source-branch tip, never the local prospective squash-merge commit exec_gate-merge produces"`
	State       string `json:"state" jsonschema:"commit status state: success, failure, error, or pending"`
	RemoteURL   string `json:"remoteUrl" jsonschema:"the github.com remote to report the status against"`
	Description string `json:"description" jsonschema:"short human-readable summary, naming which gate step failed when state is not success"`
	Context     string `json:"context,omitempty" jsonschema:"status check name a required-status-checks rule names; defaults to erun/merge-gate"`
	TargetURL   string `json:"targetUrl,omitempty" jsonschema:"optional link a reader clicks through to from the status"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, trace the request without sending it"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func execReportCommitStatusTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecReportCommitStatusInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecReportCommitStatusInput) (*mcp.CallToolResult, CommandOutput, error) {
		var reported *eruncommon.ReportCommitStatusResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			result, err := eruncommon.ReportCommitStatus(runCtx, eruncommon.ReportCommitStatusParams{
				RemoteURL:   input.RemoteURL,
				Commit:      input.Commit,
				State:       eruncommon.ReportCommitStatusState(input.State),
				Context:     input.Context,
				Description: input.Description,
				TargetURL:   input.TargetURL,
			}, eruncommon.ReportCommitStatusDependencies{})
			if err != nil {
				return err
			}
			reported = &result
			return nil
		})
		if err == nil && reported != nil {
			output.ReportCommitStatus = reported
		}
		return nil, output, err
	}
}
