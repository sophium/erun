package erunmcp

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ReleaseInput struct {
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned release actions without executing them"`
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

func releaseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReleaseInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReleaseInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (CommandOutput, error) {
			var spec eruncommon.ReleaseSpec
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx eruncommon.Context, workDir string) error {
				findProjectRoot := func() (string, string, error) {
					return runtimeFindProjectRoot(runtime.Context, workDir)
				}
				resolved, err := eruncommon.ResolveReleaseSpec(runCtx, findProjectRoot, eruncommon.ReleaseParams{})
				if err != nil {
					return err
				}
				spec = resolved
				return eruncommon.RunReleaseSpec(runCtx, spec, eruncommon.GitCommandRunner, runtime.BuildScriptRunner)
			})
			output.Spec = &spec
			return output, err
		}
		envelope, err := runJobEnvelope(runtime, "release", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
