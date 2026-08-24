package erunmcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// listTools returns the full Tool objects, not just names, so the wire metadata
// itself can be asserted.
func listTools(t *testing.T, session *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	return result.Tools
}

// TestNoToolShipsOnTheSpecDefaults is the whole point of #1186. The SDK models
// destructiveHint and openWorldHint as *bool and treats nil as TRUE, so a tool
// that declares nothing goes out as destructive and open-world. That is how
// `version` and `list` were advertised as destructive open-world tools, leaving
// a client that wanted to gate on destructiveHint gating everything or nothing.
func TestNoToolShipsOnTheSpecDefaults(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
	tools := listTools(t, session)
	if len(tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	for _, tool := range tools {
		if tool.Title == "" {
			t.Errorf("%s: no title, so a client renders the raw tool name", tool.Name)
		}
		if tool.Annotations == nil {
			t.Errorf("%s: no annotations at all, so it ships destructive and open-world by default", tool.Name)
			continue
		}
		if tool.Annotations.DestructiveHint == nil {
			t.Errorf("%s: destructiveHint unset, which the spec reads as true", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s: openWorldHint unset, which the spec reads as true", tool.Name)
		}
		if _, ok := tool.Meta["family"]; !ok {
			t.Errorf("%s: no _meta.family, so a client cannot group the surface without splitting the name", tool.Name)
		}
		_, hasPath := tool.Meta["cliPath"]
		_, mcpOnly := tool.Meta["mcpOnly"]
		if !hasPath && !mcpOnly {
			t.Errorf("%s: declares neither a cliPath nor mcpOnly, so a client cannot tell whether a command exists behind it", tool.Name)
		}
	}
}

// TestReadOnlyToolsSaySoOnTheWire: the acceptance criterion names these
// explicitly, and they are the tools whose mislabelling was most obviously
// wrong -- `version` returns a version string and was advertised as capable of
// destructive updates to an open world.
func assertToolAdvertisedReadOnly(t *testing.T, byName map[string]*mcp.Tool, name string) {
	t.Helper()
	tool, ok := byName[name]
	if !ok {
		t.Errorf("%s is not on the surface", name)
		return
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Errorf("%s: readOnlyHint is false but the tool only observes", name)
	}
	if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
		t.Errorf("%s: still advertised as destructive", name)
	}
}

func TestReadOnlyToolsSaySoOnTheWire(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
	byName := map[string]*mcp.Tool{}
	for _, tool := range listTools(t, session) {
		byName[tool.Name] = tool
	}

	for _, name := range []string{"version", "list", "exec_diff", "platform_env_list", "platform_env_get", "outputs_list"} {
		assertToolAdvertisedReadOnly(t, byName, name)
	}

	// And the counterexample, so the test cannot pass by marking everything
	// read-only: exec_raw runs arbitrary argv.
	raw, ok := byName["exec_raw"]
	if !ok {
		t.Fatal("exec_raw is not on the surface")
	}
	if raw.Annotations.ReadOnlyHint {
		t.Error("exec_raw: readOnlyHint is true, but it runs arbitrary commands")
	}
	if raw.Annotations.DestructiveHint == nil || !*raw.Annotations.DestructiveHint {
		t.Error("exec_raw: must be advertised as destructive")
	}
	if raw.Annotations.OpenWorldHint == nil || !*raw.Annotations.OpenWorldHint {
		t.Error("exec_raw: must be advertised as open-world")
	}
}

// TestRenamedToolsKeepTheirOldNamesForOneRelease: the rename is worth doing, but
// not at the cost of breaking a client pinned to `raw` on an upgrade.
func TestRenamedToolsKeepTheirOldNamesForOneRelease(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
	present := map[string]bool{}
	for _, tool := range listTools(t, session) {
		present[tool.Name] = true
	}

	for old, current := range eruncommon.MCPToolRenames() {
		if old == "workspace_sync" {
			continue // host-served by the CLI proxy, not this server.
		}
		if !present[current] {
			t.Errorf("%s: the replacement for %s is not on the surface", current, old)
		}
		if !present[old] {
			t.Errorf("%s: retired name is gone already; it must stay callable for one release", old)
		}
	}
}

// TestEveryRegisteredToolIsDescribed is the structural gate. addTool panics on a
// tool with no descriptor, so this test failing to panic is the assertion --
// and it means a tool cannot be added without a decision about its blast radius,
// which is what let the surface drift in the first place.
func TestEveryRegisteredToolIsDescribed(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
	for _, tool := range listTools(t, session) {
		if _, ok := eruncommon.MCPToolDescriptorFor(tool.Name); !ok {
			t.Errorf("%s is registered but has no descriptor; addTool should have panicked", tool.Name)
		}
	}
}
