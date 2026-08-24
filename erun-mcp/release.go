package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ReleaseInput struct {
	Preview   bool  `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned release actions without executing them"`
	Verbosity int   `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait      *bool `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func releaseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReleaseInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReleaseInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool) (CommandOutput, error) {
			var spec eruncommon.ReleaseSpec
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
				// Release resolves the same execution `erun build --release` does, so
				// it publishes the version's images and charts before it tags them.
				execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, "", "", true, false)
				if err != nil {
					return err
				}
				if resolved, ok := eruncommon.BuildExecutionReleaseSpec(execution); ok {
					spec = resolved
				}
				return eruncommon.RunReleaseExecution(runCtx, execution, eruncommon.GitCommandRunner, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime))
			})
			output.Spec = &spec
			return output, err
		}
		envelope, err := runJobEnvelope(runtime, "release", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
