package erunmcp

import (
	"context"
	"encoding/base64"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type OutputsListInput struct {
	Path  string `json:"path,omitempty" jsonschema:"pod directory to list; defaults to $ERUN_OUTPUTS_DIR (/home/erun/.erun/outputs)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of entries to return, newest-first; 0 lists all"`
}

type OutputsDownloadInput struct {
	Name    string `json:"name" jsonschema:"required entry name to download, a single path segment under the outputs directory"`
	Path    string `json:"path,omitempty" jsonschema:"pod directory the entry lives under; defaults to $ERUN_OUTPUTS_DIR (/home/erun/.erun/outputs)"`
	Preview bool   `json:"preview,omitempty" jsonschema:"when true, return the entry's metadata (name, type, size) without reading or returning its bytes"`
}

// OutputsDownloadResult is the structured result of the outputs_download tool.
// The MCP server is co-located with the files inside the pod, so bytes are
// returned inline as base64 rather than written to a host path.
type OutputsDownloadResult struct {
	Name          string `json:"name"`
	IsArchive     bool   `json:"isArchive"`
	ArchiveFormat string `json:"archiveFormat,omitempty"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256,omitempty"`
	Content       string `json:"content,omitempty"`
}

func outputsListTool() func(context.Context, *mcp.CallToolRequest, OutputsListInput) (*mcp.CallToolResult, eruncommon.RuntimeOutputsListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input OutputsListInput) (*mcp.CallToolResult, eruncommon.RuntimeOutputsListResult, error) {
		result, err := eruncommon.ResolveLocalOutputs(eruncommon.RuntimeOutputsParams{
			Dir:   outputsDirInput(input.Path),
			Limit: input.Limit,
		})
		if err != nil {
			return nil, eruncommon.RuntimeOutputsListResult{}, err
		}
		return nil, result, nil
	}
}

func outputsDownloadTool() func(context.Context, *mcp.CallToolRequest, OutputsDownloadInput) (*mcp.CallToolResult, OutputsDownloadResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input OutputsDownloadInput) (*mcp.CallToolResult, OutputsDownloadResult, error) {
		params := eruncommon.RuntimeOutputDownloadParams{
			Dir:  outputsDirInput(input.Path),
			Name: input.Name,
		}
		if input.Preview {
			out, err := eruncommon.StatLocalOutput(params)
			if err != nil {
				return nil, OutputsDownloadResult{}, err
			}
			return nil, outputsDownloadResult(out, false), nil
		}
		out, err := eruncommon.DownloadLocalOutput(params)
		if err != nil {
			return nil, OutputsDownloadResult{}, err
		}
		return nil, outputsDownloadResult(out, true), nil
	}
}

func outputsDownloadResult(out eruncommon.RuntimeOutputResult, withContent bool) OutputsDownloadResult {
	result := OutputsDownloadResult{
		Name:          out.Name,
		IsArchive:     out.IsArchive,
		ArchiveFormat: out.ArchiveFormat,
		Size:          out.Size,
		SHA256:        out.SHA256,
	}
	if withContent {
		result.Content = base64.StdEncoding.EncodeToString(out.Bytes)
	}
	return result
}

func outputsDirInput(path string) string {
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(os.Getenv(eruncommon.RuntimeOutputsDirEnvVar))
}
