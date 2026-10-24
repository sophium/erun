package erunmcp

import (
	"bytes"
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
)

type VersionInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type VersionOutput struct {
	Version string   `json:"version"`
	Commit  string   `json:"commit,omitempty"`
	Date    string   `json:"date,omitempty"`
	Trace   []string `json:"trace,omitempty"`
}

func versionTool(info model.BuildInfo) func(context.Context, *mcp.CallToolRequest, VersionInput) (*mcp.CallToolResult, VersionOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input VersionInput) (*mcp.CallToolResult, VersionOutput, error) {
		output := buildVersionOutput(info)
		traceOutput := new(bytes.Buffer)
		ctx := runtimeCallContext(false, input.Verbosity, nil, traceOutput, traceOutput)
		ctx.TraceCommand("", "erun", "version")
		output.Trace = normalizeTraceLines(traceOutput.String())
		return nil, output, nil
	}
}

func buildVersionOutput(info model.BuildInfo) VersionOutput {
	info = model.NormalizeBuildInfo(info)
	return VersionOutput{
		Version: info.Version,
		Commit:  info.Commit,
		Date:    info.Date,
	}
}
