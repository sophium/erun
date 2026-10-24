package exectool

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// CommitInput takes the commit message as data — never a shell — and the
// branch as an explicit claim to verify, mirroring `erun exec commit`.
type CommitInput struct {
	Branch    string   `json:"branch" jsonschema:"branch the caller believes the working tree is on; verified against the actual current branch and refused, loudly, on mismatch"`
	Message   string   `json:"message" jsonschema:"commit message, recorded verbatim; never shell-interpreted"`
	Paths     []string `json:"paths,omitempty" jsonschema:"when set, stage and commit only these paths instead of every change; refused if the tree has changes outside them"`
	Preview   bool     `json:"preview,omitempty" jsonschema:"when true, verify the branch and trace what would be committed without staging or committing"`
	Verbosity int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func CommitTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, CommitInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input CommitInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (shared.CommandOutput, error) {
			var committed *model.CommitWorkingTreeResult
			output, err := shared.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, workDir string) error {
				result, err := gitx.CommitWorkingTree(runCtx, workDir, model.CommitWorkingTreeParams{
					Branch:  input.Branch,
					Message: input.Message,
					Paths:   input.Paths,
				}, gitx.CommitWorkingTreeDependencies{})
				if err != nil {
					return err
				}
				committed = &result
				return nil
			})
			if err == nil && committed != nil {
				output.Commit = committed
			}
			return output, err
		}
		envelope, err := shared.RunJobEnvelope(runtime, "exec_commit", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
