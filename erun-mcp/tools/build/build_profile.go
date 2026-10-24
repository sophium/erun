package build

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build"
)

type BuildProfileInput struct {
	ID    string `json:"id,omitempty" jsonschema:"a build's id as returned by a prior call with no id (or \"latest\" for the most recent build); omitted lists recent builds instead of returning one in full"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of recent builds to list when id is omitted, newest-first (default 20); ignored when id is set"`
}

// BuildProfileResult holds either a listing (Records) or one build (Record).
// Record is raw JSON because the SDK cannot derive a schema for the
// self-referential step type.
type BuildProfileResult struct {
	Records []model.TimingRecordSummary `json:"records,omitempty"`
	Record  json.RawMessage             `json:"record,omitempty"`
}

const buildProfileMCPCommandName = "build"

func BuildProfileTool() func(context.Context, *mcp.CallToolRequest, BuildProfileInput) (*mcp.CallToolResult, BuildProfileResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildProfileInput) (*mcp.CallToolResult, BuildProfileResult, error) {
		if input.ID == "" {
			records, err := build.ListTimingRecords(buildProfileMCPCommandName, input.Limit)
			if err != nil {
				return nil, BuildProfileResult{}, err
			}
			return nil, BuildProfileResult{Records: records}, nil
		}
		record, err := build.LoadTimingRecord(buildProfileMCPCommandName, input.ID)
		if err != nil {
			return nil, BuildProfileResult{}, err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, BuildProfileResult{}, err
		}
		return nil, BuildProfileResult{Record: encoded}, nil
	}
}
