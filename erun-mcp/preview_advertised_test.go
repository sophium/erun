package erunmcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/registrar"
)

// previewAdvertisedInSchema reads the wire schema, not the Go type.
func previewAdvertisedInSchema(t *testing.T, tool *mcp.Tool) bool {
	t.Helper()
	if tool.InputSchema == nil {
		return false
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: cannot read input schema: %v", tool.Name, err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%s: input schema is not an object schema: %v", tool.Name, err)
	}
	_, ok := schema.Properties["preview"]
	return ok
}

func previewAdvertisedInDescription(tool *mcp.Tool) bool {
	return strings.HasSuffix(strings.TrimSpace(tool.Description), registrar.PreviewAdvertised)
}

// Assert a non-zero count so the loop cannot pass vacuously on an empty list.
func TestPreviewIsAdvertisedExactlyWhereItIsAccepted(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin))
	tools := listTools(t, session)
	if len(tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	accepting, advertising := 0, 0
	for _, tool := range tools {
		accepts := previewAdvertisedInSchema(t, tool)
		advertises := previewAdvertisedInDescription(tool)
		if accepts {
			accepting++
		}
		if advertises {
			advertising++
		}
		switch {
		case accepts && !advertises:
			t.Errorf("%s accepts preview but does not say so, so a reader takes it for a tool with no dry run", tool.Name)
		case advertises && !accepts:
			t.Errorf("%s advertises preview but its input schema has no preview property, so it promises a dry run it cannot perform", tool.Name)
		}
	}

	t.Logf("tools: %d total, %d accept preview, %d advertise it", len(tools), accepting, advertising)

	if accepting == 0 {
		t.Fatal("no tool accepts preview, so the biconditional above proves nothing")
	}
}

// embeddedPreview reaches `preview` through an embedded struct.
type embeddedPreview struct {
	Preview bool `json:"preview,omitempty"`
}

type promotedPreview struct {
	embeddedPreview
	Other bool `json:"other,omitempty"`
}

type noPreview struct {
	Other bool `json:"other,omitempty"`
}

func TestAdvertisePreviewFollowsTheInputType(t *testing.T) {
	declared := &mcp.Tool{Name: "declared", Description: "Does a thing."}
	registrar.AdvertisePreview[struct {
		Preview bool `json:"preview,omitempty"`
	}](declared)

	promoted := &mcp.Tool{Name: "promoted", Description: "Does a thing."}
	registrar.AdvertisePreview[promotedPreview](promoted)

	absent := &mcp.Tool{Name: "absent", Description: "Does a thing."}
	registrar.AdvertisePreview[noPreview](absent)

	for _, tool := range []*mcp.Tool{declared, promoted} {
		if !previewAdvertisedInDescription(tool) {
			t.Errorf("%s: accepts preview but the description does not say so: %q", tool.Name, tool.Description)
		}
		if n := strings.Count(tool.Description, registrar.PreviewAdvertised); n != 1 {
			t.Errorf("%s: sentence appears %d times, want 1: %q", tool.Name, n, tool.Description)
		}
	}

	if previewAdvertisedInDescription(absent) {
		t.Errorf("absent: description advertises preview but the input type has no such property: %q", absent.Description)
	}

	// Re-registering must not append the sentence twice.
	registrar.AdvertisePreview[promotedPreview](promoted)
	if n := strings.Count(promoted.Description, registrar.PreviewAdvertised); n != 1 {
		t.Errorf("promoted: re-registering produced the sentence %d times: %q", n, promoted.Description)
	}
}

// No registered tool trips this guard, so it is exercised directly.
func TestAdvertisePreviewRefusesAFalseClaim(t *testing.T) {
	tool := &mcp.Tool{Name: "synthetic", Description: "Does a thing. " + registrar.PreviewAdvertised}

	defer func() {
		if recover() == nil {
			t.Error("advertisePreview accepted a description claiming a preview its input type cannot deliver, so the claim would ship")
		}
	}()

	registrar.AdvertisePreview[noPreview](tool)
}

func TestDestructiveToolsAdvertisePreview(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin))
	byName := map[string]*mcp.Tool{}
	for _, tool := range listTools(t, session) {
		byName[tool.Name] = tool
	}

	for _, name := range []string{"deploy", "delete", "doctor", "build", "exec_commit", "exec_gate-merge"} {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("%s is not on the surface", name)
			continue
		}
		if !previewAdvertisedInSchema(t, tool) {
			continue // Names the behaviour, not the schema; covered by the biconditional test.
		}
		if !previewAdvertisedInDescription(tool) {
			t.Errorf("%s accepts preview but its description does not say so", name)
		}
	}
}
