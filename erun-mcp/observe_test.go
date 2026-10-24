package erunmcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func observeOutputSchema(t *testing.T, session *mcp.ClientSession) *jsonschema.Schema {
	t.Helper()
	for _, tool := range listTools(t, session) {
		if tool.Name != "observe" {
			continue
		}
		if tool.OutputSchema == nil {
			t.Fatal("observe has no output schema")
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal published output schema: %v", err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal published output schema: %v", err)
		}
		return &schema
	}
	t.Fatal("observe is not in tools/list")
	return nil
}

func TestObserveToolRequiresTheDriftProperty(t *testing.T) {
	schema := observeOutputSchema(t, connectWithCapabilities(t, string(mcptools.MCPCapabilityRead)))

	if !slices.Contains(schema.Required, "drift") {
		t.Fatalf("published outputSchema does not require `drift`; required = %v", schema.Required)
	}
	drift, ok := schema.Properties["drift"]
	if !ok {
		t.Fatal("published outputSchema has no `drift` property at all")
	}
	if !slices.Contains(drift.Types, "array") {
		t.Fatalf("`drift` is not an array: types = %v", drift.Types)
	}
	if drift.Items == nil || drift.Items.Type != "string" {
		t.Fatalf("`drift` does not carry string findings: items = %+v", drift.Items)
	}
}

// A preview reads nothing, so drift is null, but the key must still be present.
func TestObserveToolReturnsTheDriftKey(t *testing.T) {
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}
	session := connectWithRuntime(t, runtime, string(mcptools.MCPCapabilityRead))

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "observe",
		Arguments: map[string]any{"preview": true},
	})
	if err != nil {
		t.Fatalf("observe call: %v", err)
	}
	if result.IsError {
		t.Fatalf("observe call reported an error: %+v", result.Content)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	value, ok := payload["drift"]
	if !ok {
		t.Fatalf("structured content has no `drift` key: %s", raw)
	}
	if string(value) != "null" {
		t.Fatalf("a preview read nothing, so its verdict must be null, got %s", value)
	}
}
