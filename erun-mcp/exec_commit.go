package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// CommitInput takes the commit message as data — never a shell — and the
// branch as an explicit claim to verify, mirroring `erun exec commit`.
type CommitInput struct {
	Branch    string   `json:"branch" jsonschema:"branch the caller believes the working tree is on; verified against the actual current branch and refused, loudly, on mismatch"`
	Message   string   `json:"message" jsonschema:"commit message, recorded verbatim; never shell-interpreted"`
	Paths     []string `json:"paths,omitempty" jsonschema:"when set, stage and commit only these paths instead of every change; refused if the tree has changes outside them"`
	Preview   bool     `json:"preview,omitempty" jsonschema:"when true, verify the branch and trace what would be committed without staging or committing"`
	Verbosity int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func commitTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CommitInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CommitInput) (*mcp.CallToolResult, CommandOutput, error) {
		var committed *eruncommon.CommitWorkingTreeResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			result, err := eruncommon.CommitWorkingTree(runCtx, workDir, eruncommon.CommitWorkingTreeParams{
				Branch:  input.Branch,
				Message: input.Message,
				Paths:   input.Paths,
			}, eruncommon.CommitWorkingTreeDependencies{})
			if err != nil {
				return err
			}
			committed = &result
			return nil
		})
		if err == nil && committed != nil {
			output.Commit = committed
		}
		return nil, output, err
	}
}
