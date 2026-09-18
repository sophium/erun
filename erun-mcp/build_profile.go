package erunmcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type BuildProfileInput struct {
	ID    string `json:"id,omitempty" jsonschema:"a build's id as returned by a prior call with no id (or \"latest\" for the most recent build); omitted lists recent builds instead of returning one in full"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of recent builds to list when id is omitted, newest-first (default 20); ignored when id is set"`
}

// BuildProfileResult is one of two shapes depending on whether Input.ID was
// set: a recent-builds listing (Records, Record left empty), or one build's
// full step tree (Record, Records left empty). Folding both into one result
// type keeps this a single tool rather than a list/show pair, matching how
// few fields either shape has. Record is a raw eruncommon.TimingRecord
// captured verbatim rather than the typed struct: TimingStepJSON nests itself
// (a step's own Steps field), and the SDK's schema reflector cannot compute
// an output schema for a directly self-referential type -- capabilities.go's
// rawJSONSchemaOverrides already widens json.RawMessage to accept any JSON
// for exactly this reason (see EnvironmentJob.Result).
type BuildProfileResult struct {
	Records []eruncommon.TimingRecordSummary `json:"records,omitempty"`
	Record  json.RawMessage                  `json:"record,omitempty"`
}

const buildProfileMCPCommandName = "build"

func buildProfileTool() func(context.Context, *mcp.CallToolRequest, BuildProfileInput) (*mcp.CallToolResult, BuildProfileResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildProfileInput) (*mcp.CallToolResult, BuildProfileResult, error) {
		if input.ID == "" {
			records, err := eruncommon.ListTimingRecords(buildProfileMCPCommandName, input.Limit)
			if err != nil {
				return nil, BuildProfileResult{}, err
			}
			return nil, BuildProfileResult{Records: records}, nil
		}
		record, err := eruncommon.LoadTimingRecord(buildProfileMCPCommandName, input.ID)
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
