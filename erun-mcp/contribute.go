package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ContributeCloneInput is the MCP tool input for `contribute_clone`.
// preview maps to the CLI's --dry-run flag.
type ContributeCloneInput struct {
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, only resolve and trace the plan without cloning"`
	JobEnvelopeInput
}

func contributeCloneTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContributeCloneInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContributeCloneInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := simpleJobExecute(runtime, input.Verbosity, func(ctx eruncommon.Context, _ string) error {
			return eruncommon.RunContributeClone(ctx, "", eruncommon.GitCommandRunner)
		})
		envelope, err := runJobEnvelope(runtime, "contribute_clone", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
