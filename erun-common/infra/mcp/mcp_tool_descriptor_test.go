package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// toolsListPayload holds real descriptors, one destructive and one read-only.
const toolsListPayload = `{
  "tools": [
    {
      "_meta": {"mcpOnly": true, "family": "cloud"},
      "annotations": {"destructiveHint": true, "idempotentHint": false, "openWorldHint": true, "readOnlyHint": false},
      "description": "Clear the AWS credentials delivered to this environment.",
      "inputSchema": {"type": "object", "properties": {}},
      "name": "cloud_clear_aws_credentials",
      "outputSchema": {"type": "object", "properties": {"cleared": {"type": "boolean"}}},
      "title": "Clear AWS credentials"
    },
    {
      "_meta": {"mcpOnly": true, "family": "cloud"},
      "annotations": {"destructiveHint": false, "readOnlyHint": true},
      "description": "List the environments in the platform.",
      "inputSchema": {"type": "object", "properties": {}},
      "name": "cloud_list",
      "outputSchema": {"type": "object"},
      "title": "List environments"
    }
  ]
}`

// Compare whole descriptors so a field the protocol adds later cannot vanish unnoticed.
func TestMCPToolListResultRoundTripsEveryProtocolField(t *testing.T) {
	encoded := encodeToolList(t, toolsListPayload)
	got, want := decodeToolsByName(t, encoded), decodeToolsByName(t, []byte(toolsListPayload))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("structured tool list does not match the protocol descriptor\n got: %v\nwant: %v", got, want)
	}
}

func TestMCPToolListResultExposesAnnotationsToStructuredCallers(t *testing.T) {
	encoded := encodeToolList(t, toolsListPayload)
	destructive := annotationsForTool(t, encoded, "cloud_clear_aws_credentials")
	if destructive.destructive == nil || !*destructive.destructive {
		t.Errorf("cloud_clear_aws_credentials destructiveHint = %v, want true", destructive.destructive)
	}
	readOnly := annotationsForTool(t, encoded, "cloud_list")
	if readOnly.readOnly == nil || !*readOnly.readOnly {
		t.Errorf("cloud_list readOnlyHint = %v, want true", readOnly.readOnly)
	}
	if readOnly.destructive == nil || *readOnly.destructive {
		t.Errorf("cloud_list destructiveHint = %v, want false", readOnly.destructive)
	}
}

type toolHints struct {
	destructive *bool
	readOnly    *bool
}

func encodeToolList(t *testing.T, payload string) []byte {
	t.Helper()
	var decoded MCPToolListResult
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode tools/list payload: %v", err)
	}
	if len(decoded.Tools) == 0 {
		t.Fatal("decoded no tools from the tools/list payload")
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode tool list result: %v", err)
	}
	return encoded
}

func annotationsForTool(t *testing.T, payload []byte, name string) toolHints {
	t.Helper()
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations *struct {
				Destructive *bool `json:"destructiveHint"`
				ReadOnly    *bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatalf("decode structured tool list: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != name {
			continue
		}
		if tool.Annotations == nil {
			t.Fatalf("tool %s reached the structured surface without annotations", name)
		}
		return toolHints{destructive: tool.Annotations.Destructive, readOnly: tool.Annotations.ReadOnly}
	}
	t.Fatalf("tool %s is missing from the structured tool list", name)
	return toolHints{}
}

func decodeToolsByName(t *testing.T, payload []byte) map[string]map[string]any {
	t.Helper()
	var envelope struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode tools payload: %v", err)
	}
	byName := make(map[string]map[string]any, len(envelope.Tools))
	for _, tool := range envelope.Tools {
		name, _ := tool["name"].(string)
		byName[name] = tool
	}
	return byName
}
