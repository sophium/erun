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
		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, ReleaseOutput{}, err
		}

		findProjectRoot := func() (string, string, error) {
			return runtimeFindProjectRoot(runtime.Context, workDir)
		}
		spec, err := eruncommon.ResolveReleaseSpec(findProjectRoot, eruncommon.ReleaseParams{})
		if err != nil {
			return nil, ReleaseOutput{}, err
		}

		commandOutput, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, _ string) error {
			return eruncommon.RunReleaseSpec(runCtx, spec, eruncommon.GitCommandRunner)
		})
		if err != nil {
			return nil, ReleaseOutput{CommandOutput: commandOutput, Spec: spec}, err
		}

		return nil, ReleaseOutput{CommandOutput: commandOutput, Spec: spec}, nil
	}
}
