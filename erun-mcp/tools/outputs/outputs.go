package outputs

import (
	"context"
	"encoding/base64"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/command/jobs"
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
	// Signing is present only when the download ran on a macOS host and ad-hoc
	// code signing had something to report. macOS kills an unsigned Mach-O with
	// SIGKILL and no message, so a caller must be able to see what was done.
	Signing *eruncommon.HostArtifactSigning `json:"signing,omitempty"`
}

func OutputsListTool() func(context.Context, *mcp.CallToolRequest, OutputsListInput) (*mcp.CallToolResult, jobs.RuntimeOutputsListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input OutputsListInput) (*mcp.CallToolResult, jobs.RuntimeOutputsListResult, error) {
		result, err := jobs.ResolveLocalOutputs(jobs.RuntimeOutputsParams{
			Dir:   outputsDirInput(input.Path),
			Limit: input.Limit,
		})
		if err != nil {
			return nil, jobs.RuntimeOutputsListResult{}, err
		}
		return nil, result, nil
	}
}

func OutputsDownloadTool() func(context.Context, *mcp.CallToolRequest, OutputsDownloadInput) (*mcp.CallToolResult, OutputsDownloadResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input OutputsDownloadInput) (*mcp.CallToolResult, OutputsDownloadResult, error) {
		params := jobs.RuntimeOutputDownloadParams{
			Dir:  outputsDirInput(input.Path),
			Name: input.Name,
		}
		if input.Preview {
			out, err := jobs.StatLocalOutput(params)
			if err != nil {
				return nil, OutputsDownloadResult{}, err
			}
			return nil, outputsDownloadResult(out, false), nil
		}
		out, err := jobs.DownloadLocalOutput(params)
		if err != nil {
			return nil, OutputsDownloadResult{}, err
		}
		return nil, outputsDownloadResult(out, true), nil
	}
}

func outputsDownloadResult(out jobs.RuntimeOutputResult, withContent bool) OutputsDownloadResult {
	result := OutputsDownloadResult{
		Name:          out.Name,
		IsArchive:     out.IsArchive,
		ArchiveFormat: out.ArchiveFormat,
		Size:          out.Size,
		SHA256:        out.SHA256,
		Signing:       out.Signing,
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
