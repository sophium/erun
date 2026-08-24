package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ContributeCloneInput is the MCP tool input for `contribute_clone`.
// preview maps to the CLI's --dry-run flag.
type ContributeCloneInput struct {
	Verbosity int   `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Preview   bool  `json:"preview,omitempty" jsonschema:"when true, only resolve and trace the plan without cloning"`
	Wait      *bool `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func contributeCloneTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContributeCloneInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContributeCloneInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := simpleJobExecute(runtime, input.Verbosity, func(ctx eruncommon.Context, _ string) error {
			return eruncommon.RunContributeClone(ctx, "", eruncommon.GitCommandRunner)
		})
		envelope, err := runJobEnvelope(runtime, "contribute_clone", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
