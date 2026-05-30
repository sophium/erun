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
}

func contributeCloneTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ContributeCloneInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ContributeCloneInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(ctx eruncommon.Context, _ string) error {
			return eruncommon.RunContributeClone(ctx, "", eruncommon.GitCommandRunner)
		})
		return nil, output, err
	}
}
