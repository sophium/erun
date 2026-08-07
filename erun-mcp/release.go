package erunmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ReleaseInput struct {
	Preview   bool `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned release actions without executing them"`
	Verbosity int  `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type ReleaseOutput struct {
	CommandOutput
	Spec eruncommon.ReleaseSpec `json:"spec"`
}

func releaseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReleaseInput) (*mcp.CallToolResult, ReleaseOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReleaseInput) (*mcp.CallToolResult, ReleaseOutput, error) {
		var spec eruncommon.ReleaseSpec
		commandOutput, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
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
		if err != nil {
			return nil, ReleaseOutput{CommandOutput: commandOutput, Spec: spec}, err
		}

		return nil, ReleaseOutput{CommandOutput: commandOutput, Spec: spec}, nil
	}
}
