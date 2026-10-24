package build

import (
	"context"
	"errors"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	executepkg "github.com/sophium/erun/erun-common/orchestration/execute"
	"github.com/sophium/erun/erun-common/primitive/release"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

type ReleaseInput struct {
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned release actions without executing them"`
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	sharedpkg.JobEnvelopeInput
}

func ReleaseTool(runtime sharedpkg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReleaseInput) (*mcp.CallToolResult, sharedpkg.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReleaseInput) (*mcp.CallToolResult, sharedpkg.JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (sharedpkg.CommandOutput, error) {
			var spec model.ReleaseSpec
			output, err := sharedpkg.RunRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, workDir string) error {
				// Release only stamps, tags and pushes; it never builds or publishes.
				execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, "", "", true, false, nil)
				if err != nil {
					return err
				}
				resolved, ok := executepkg.BuildExecutionReleaseSpec(execution)
				if !ok {
					return errors.New("release: the resolved plan is not a release")
				}
				spec = resolved
				return release.RunReleaseSpec(runCtx, spec, gitx.GitCommandRunner)
			})
			output.Spec = &spec
			return output, err
		}
		envelope, err := sharedpkg.RunJobEnvelope(runtime, "release", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
