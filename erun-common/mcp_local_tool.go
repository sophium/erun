package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
)

// MCPLocalTool is a tool the stdio proxy answers itself instead of relaying to
// the environment's edge. Almost everything belongs in the pod, where the
// environment's own toolchain is; the exception is work whose subject lives on
// this host — the review mirror — which the edge structurally cannot reach. A
// client sees no difference: the tool is listed and called like any other.
type MCPLocalTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Call        func(ctx context.Context, arguments json.RawMessage) (string, error)
}

type mcpToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type mcpToolsListResult struct {
	Tools []json.RawMessage `json:"tools"`
}

type mcpRequestEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"params"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolCallResult struct {
	Content []mcpTextContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type mcpReplyEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
}

// mcpLocalToolFor reports the local tool a tools/call message addresses, so the
// proxy can answer it without a round trip to the edge.
func mcpLocalToolFor(message []byte, tools []MCPLocalTool) (MCPLocalTool, json.RawMessage, bool) {
	if len(tools) == 0 {
		return MCPLocalTool{}, nil, false
	}
	var envelope mcpRequestEnvelope
	if err := json.Unmarshal(message, &envelope); err != nil || envelope.Method != "tools/call" {
		return MCPLocalTool{}, nil, false
	}
	for _, tool := range tools {
		if tool.Name == envelope.Params.Name {
			return tool, envelope.Params.Arguments, true
		}
	}
	return MCPLocalTool{}, nil, false
}

func mcpIsToolsList(message []byte) bool {
	var envelope mcpRequestEnvelope
	return json.Unmarshal(message, &envelope) == nil && envelope.Method == "tools/list"
}

// mcpAppendLocalTools merges the locally served tools into the edge's tools/list
// reply, so a client discovers them the only way it can. A reply it cannot parse
// is passed through untouched rather than dropped.
func mcpAppendLocalTools(reply []byte, tools []MCPLocalTool) []byte {
	if len(tools) == 0 {
		return reply
	}
	var envelope mcpReplyEnvelope
	if err := json.Unmarshal(reply, &envelope); err != nil || len(envelope.Result) == 0 {
		return reply
	}
	var listed mcpToolsListResult
	if err := json.Unmarshal(envelope.Result, &listed); err != nil {
		return reply
	}
	for _, tool := range tools {
		encoded, err := json.Marshal(mcpToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
		if err != nil {
			continue
		}
		listed.Tools = append(listed.Tools, encoded)
	}
	result, err := json.Marshal(listed)
	if err != nil {
		return reply
	}
	envelope.Result = result
	merged, err := json.Marshal(envelope)
	if err != nil {
		return reply
	}
	return merged
}

// mcpLocalToolReply renders a local tool's outcome in the shape a client expects
// from any tool, including a failure: an error is content the model reads and
// can act on, not a transport fault.
func mcpLocalToolReply(id json.RawMessage, text string, callErr error) ([]byte, error) {
	result := mcpToolCallResult{Content: []mcpTextContent{{Type: "text", Text: text}}}
	if callErr != nil {
		result = mcpToolCallResult{
			Content: []mcpTextContent{{Type: "text", Text: callErr.Error()}},
			IsError: true,
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode local tool result: %w", err)
	}
	return json.Marshal(mcpReplyEnvelope{JSONRPC: "2.0", ID: id, Result: encoded})
}
