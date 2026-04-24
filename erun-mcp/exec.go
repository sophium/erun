package erunmcp

import (
	"bytes"
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type RawInput struct {
	Command   []string `json:"command" jsonschema:"command and arguments to execute from the runtime repo root"`
	Stdin     string   `json:"stdin,omitempty" jsonschema:"optional stdin to pass to the command"`
	Preview   bool     `json:"preview,omitempty" jsonschema:"when true, trace the command without executing it"`
	Verbosity int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func rawTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, RawInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input RawInput) (*mcp.CallToolResult, CommandOutput, error) {
		traceOutput := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, input.Verbosity, strings.NewReader(input.Stdin), traceOutput, traceOutput)

		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, CommandOutput{}, err
		}

		output, err := runCommandOutput(ctx, workDir, traceOutput, func(runCtx eruncommon.Context) error {
			return eruncommon.RunRawCommand(runCtx, eruncommon.RawCommandSpec{
				Dir:  workDir,
				Args: input.Command,
			}, nil)
		})
		return nil, output, err
	}
}
