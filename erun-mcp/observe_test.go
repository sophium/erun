package erunmcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The observe tool published `drift` in its outputSchema while no surface
// ever returned it — `erun observe --output json` left the key out entirely
// and this tool's structured content did too, so a client that generated
// types from the schema got a field it could never read and no way to tell
// "no drift this run" from "this key is never populated". The two tests below
// pin the two halves of that contract against the schema and the payload the
// server really publishes, not a hand-written expectation.

// observeOutputSchema returns the output schema the server publishes for
// `observe` over the wire (tools/list).
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

// TestObserveToolRequiresTheDriftProperty covers the schema half: the published
// contract must say the verdict is always there, so a generated client models
// it as present rather than as a field it may never see.
func TestObserveToolRequiresTheDriftProperty(t *testing.T) {
	schema := observeOutputSchema(t, connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead)))

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

// TestObserveToolReturnsTheDriftKey covers the payload half against the tool
// itself: a call's structured content must carry the key, so a consumer
// reads the verdict off the same call it reads the rest of the state from.
//
// The call previews, which reads nothing: the key is then null — "not
// determined", distinct from the [] a completed comparison carries when
// nothing disagreed and from a populated list when something did. The
// populated and empty cases are pinned where they are produced
// (erun-common's observe_drift_test.go and erun-integration's observe
// goldens); what this pins is that the key itself is never dropped at the MCP
// boundary.
func TestObserveToolReturnsTheDriftKey(t *testing.T) {
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		// The module's shared local-target store fixture, which resolves the
		// one environment this server would be scoped to.
		Store: usageTestStore("tenant-a", "dev"),
	}
	session := connectWithRuntime(t, runtime, string(eruncommon.MCPCapabilityRead))

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
