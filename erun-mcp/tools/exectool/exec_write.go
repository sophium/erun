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

// WriteInput takes file content as data — never a shell — mirroring `erun
// exec write`.
type WriteInput struct {
	Path      string `json:"path" jsonschema:"destination path inside the runtime repo root, absolute or relative; refused if it would resolve outside the repo root"`
	Content   string `json:"content" jsonschema:"file content, written verbatim byte-for-byte; never shell-interpreted"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the write without performing it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func WriteTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, WriteInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input WriteInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (shared.CommandOutput, error) {
			var written *model.WriteWorkingTreeFileResult
			output, err := shared.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, workDir string) error {
				result, err := gitx.WriteWorkingTreeFile(runCtx, workDir, model.WriteWorkingTreeFileParams{
					Path:    input.Path,
					Content: input.Content,
				})
				if err != nil {
					return err
				}
				written = &result
				return nil
			})
			if err == nil && written != nil {
				output.Write = written
			}
			return output, err
		}
		envelope, err := shared.RunJobEnvelope(runtime, "exec_write", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
