package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// WriteInput takes file content as data — never a shell — mirroring `erun
// exec write`.
type WriteInput struct {
	Path      string `json:"path" jsonschema:"destination path inside the runtime repo root, absolute or relative; refused if it would resolve outside the repo root"`
	Content   string `json:"content" jsonschema:"file content, written verbatim byte-for-byte; never shell-interpreted"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the write without performing it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait      *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func writeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, WriteInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input WriteInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool) (CommandOutput, error) {
			var written *eruncommon.WriteWorkingTreeFileResult
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
				result, err := eruncommon.WriteWorkingTreeFile(runCtx, workDir, eruncommon.WriteWorkingTreeFileParams{
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
		envelope, err := runJobEnvelope(runtime, "exec_write", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
