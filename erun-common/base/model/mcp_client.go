package model

import (
	"encoding/json"
)

type MCPToolCallResult struct {
	Tool       string          `json:"tool"`
	Text       string          `json:"text,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

// MCPTool keeps the descriptor's wire JSON so structured output re-emits the protocol's own
// shape; the typed fields serve the text renderer.
type MCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	// raw is empty only for a tool constructed locally.
	raw json.RawMessage
}

func (t *MCPTool) UnmarshalJSON(data []byte) error {
	var fields struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	t.Name = fields.Name
	t.Description = fields.Description
	t.InputSchema = fields.InputSchema
	t.raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON re-emits the decoded descriptor verbatim so unknown fields survive.
func (t MCPTool) MarshalJSON() ([]byte, error) {
	if len(t.raw) > 0 {
		return t.raw, nil
	}
	type constructedTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	}
	return json.Marshal(constructedTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
}

type MCPToolListResult struct {
	Tools []MCPTool `json:"tools"`
}
