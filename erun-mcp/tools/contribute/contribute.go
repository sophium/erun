package contribute

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// ContributeCloneInput's Preview maps to the CLI's --dry-run flag.
type ContributeCloneInput struct {
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, only resolve and trace the plan without cloning"`
	shared.JobEnvelopeInput
}

func ContributeCloneTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContributeCloneInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContributeCloneInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := shared.SimpleJobExecute(runtime, input.Verbosity, func(ctx core.Context, _ string) error {
			return review.RunContributeClone(ctx, "", gitx.GitCommandRunner)
		})
		envelope, err := shared.RunJobEnvelope(runtime, "contribute_clone", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
