package build

import (
	"context"
	"errors"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type ReleaseInput struct {
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned release actions without executing them"`
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func ReleaseTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReleaseInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReleaseInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (shared.CommandOutput, error) {
			var spec eruncommon.ReleaseSpec
			output, err := shared.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, workDir string) error {
				// Release marks source control only: it stamps, tags and pushes the
				// version and never builds or publishes an artifact. Build and
				// publish are `build --release` and `push --version`.
				execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, "", "", true, false, nil)
				if err != nil {
					return err
				}
				resolved, ok := eruncommon.BuildExecutionReleaseSpec(execution)
				if !ok {
					return errors.New("release: the resolved plan is not a release")
				}
				spec = resolved
				return eruncommon.RunReleaseSpec(runCtx, spec, gitx.GitCommandRunner)
			})
			output.Spec = &spec
			return output, err
		}
		envelope, err := shared.RunJobEnvelope(runtime, "release", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
