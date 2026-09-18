package erunmcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// previewAdvertisedInSchema reads the property out of the schema a client
// actually receives, rather than off the Go type, so the assertion is about the
// wire contract and not about which struct the registration happens to use.
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

// previewAdvertisedInDescription reports whether the description carries the
// sentence, matching how a reader scans for it: as the closing claim.
func previewAdvertisedInDescription(tool *mcp.Tool) bool {
	return strings.HasSuffix(strings.TrimSpace(tool.Description), previewAdvertised)
}

// TestPreviewIsAdvertisedExactlyWhereItIsAccepted pins the biconditional over
// the whole surface, in both directions.
//
// The defect this replaces was one-directional and therefore easy to miss: 22
// tools said "Supports preview." and 58 with the same `preview` property said
// nothing, which reads as a capability boundary rather than an oversight. A
// test that only checked the 22 still say it would have passed throughout.
//
// The count is asserted non-zero so the loop cannot pass by finding nothing to
// check -- the failure mode a surface-wide test has when the session returns an
// empty list.
func TestPreviewIsAdvertisedExactlyWhereItIsAccepted(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
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

// TestDestructiveToolsAdvertisePreview names the tools whose silence cost the
// most. A caller rehearsing a mutating reconcile reaches for these first, and
// `doctor --sync-config` was run against a live environment because its
// description gave no hint that a preview existed.
func TestDestructiveToolsAdvertisePreview(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
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
