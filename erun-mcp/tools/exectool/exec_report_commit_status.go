package exectool

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// ExecReportCommitStatusInput reports a merge queue build as a GitHub commit status.
type ExecReportCommitStatusInput struct {
	Commit      string `json:"commit" jsonschema:"full commit SHA the status attaches to — the review's source-branch tip, never the local prospective squash-merge commit exec_gate-merge produces"`
	State       string `json:"state" jsonschema:"commit status state: success, failure, error, or pending"`
	RemoteURL   string `json:"remoteUrl" jsonschema:"the github.com remote to report the status against"`
	Description string `json:"description" jsonschema:"short human-readable summary, naming which step failed when state is not success"`
	Context     string `json:"context,omitempty" jsonschema:"status check name a required-status-checks rule names; defaults to erun/merge-gate"`
	TargetURL   string `json:"targetUrl,omitempty" jsonschema:"optional link a reader clicks through to from the status"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, trace the request without sending it"`
	Verbosity   int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func ExecReportCommitStatusTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecReportCommitStatusInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecReportCommitStatusInput) (*mcp.CallToolResult, shared.CommandOutput, error) {
		var reported *model.ReportCommitStatusResult
		output, err := shared.RunRuntimeCommand(runtime, input.Preview, input.Verbosity, nil, func(runCtx core.Context, _ string) error {
			result, err := review.ReportCommitStatus(runCtx, model.ReportCommitStatusParams{
				RemoteURL:   input.RemoteURL,
				Commit:      input.Commit,
				State:       model.ReportCommitStatusState(input.State),
				Context:     input.Context,
				Description: input.Description,
				TargetURL:   input.TargetURL,
			}, review.ReportCommitStatusDependencies{})
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
