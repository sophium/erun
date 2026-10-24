package mcptools

import (
	"strings"
	"testing"
)

// Guards against a tool like `erun exec raw` surfacing as `raw`.
func TestToolNameEqualsItsCLIPath(t *testing.T) {
	for _, name := range MCPToolNames() {
		descriptor, ok := MCPToolDescriptorFor(name)
		if !ok {
			t.Fatalf("%s: MCPToolNames returned a name the table does not describe", name)
		}
		if descriptor.CLIPath == nil {
			continue // wire-only; TestMCPOnlyToolsAreTheKnownSet pins these.
		}
		if joined := strings.Join(descriptor.CLIPath, "_"); joined != name {
			t.Errorf("%s: CLI path %v joins to %q; a tool's name must equal its CLI path with _ for spaces", name, descriptor.CLIPath, joined)
		}
	}
}

// Adding a tool with no CLI path must be a deliberate decision.
func TestMCPOnlyToolsAreTheKnownSet(t *testing.T) {
	want := map[string]struct{}{
		"activity_lease_list":          {},
		"activity_lease_take":          {},
		"activity_lease_release":       {},
		"ai_sessions":                  {},
		"environment":                  {},
		"idle_stop_history":            {},
		"idle_stop_record":             {},
		"idle_stop_cancel":             {},
		"cloud_list":                   {},
		"cloud_inject_aws_credentials": {},
		"cloud_clear_aws_credentials":  {},
		"terraform":                    {},
		"exec_agent":                   {},
		"job_start":                    {},
	}
	for _, name := range MCPToolNames() {
		_, expected := want[name]
		if MCPToolIsMCPOnly(name) != expected {
			if expected {
				t.Errorf("%s: expected to be MCP-only but it declares a CLI path", name)
			} else {
				t.Errorf("%s: has no CLI path; either give it one or add it to the known MCP-only set deliberately", name)
			}
		}
	}
}

// The capability allowlist is stricter than ReadOnly, but a tool a read-only
// token may call must never modify its environment.
func TestMCPReadOnlyToolsAreSemanticallyReadOnly(t *testing.T) {
	for name := range mcpReadOnlyTools {
		descriptor, ok := MCPToolDescriptorFor(name)
		if !ok {
			t.Errorf("%s is in the read-only capability allowlist but has no descriptor", name)
			continue
		}
		if !descriptor.ReadOnly {
			t.Errorf("%s is callable by a read-only token but its descriptor says it modifies its environment; one of the two is wrong", name)
		}
	}
}

func TestRenamesResolveToRealTools(t *testing.T) {
	for old, current := range MCPToolRenames() {
		if _, ok := mcpToolDescriptors[current]; !ok {
			t.Errorf("rename %s -> %s: the replacement is not a described tool", old, current)
		}
		if _, ok := mcpToolDescriptors[old]; ok {
			t.Errorf("rename %s -> %s: the retired name is still in the descriptor table, so it would shadow the replacement", old, current)
		}
		if got := MCPToolCurrentName(old); got != current {
			t.Errorf("MCPToolCurrentName(%q) = %q, want %q", old, got, current)
		}
	}
}

// Without resolving the retired name first, diff would silently require admin.
func TestRetiredNameAuthorizesAsItsReplacement(t *testing.T) {
	if got := MCPToolCapability("diff"); got != MCPCapabilityRead {
		t.Errorf("MCPToolCapability(\"diff\") = %q, want %q -- the retired name must authorize as exec_diff does", got, MCPCapabilityRead)
	}
	if got := MCPToolCapability("exec_diff"); got != MCPCapabilityRead {
		t.Errorf("MCPToolCapability(\"exec_diff\") = %q, want %q", got, MCPCapabilityRead)
	}
}

// A client renders title in preference to name.
func TestEveryToolDeclaresATitle(t *testing.T) {
	for _, name := range MCPToolNames() {
		descriptor, _ := MCPToolDescriptorFor(name)
		if strings.TrimSpace(descriptor.Title) == "" {
			t.Errorf("%s declares no title", name)
		}
	}
}

func TestReadOnlyToolsAreNotAlsoDestructive(t *testing.T) {
	for _, name := range MCPToolNames() {
		descriptor, _ := MCPToolDescriptorFor(name)
		if descriptor.ReadOnly && descriptor.Destructive {
			t.Errorf("%s claims to be both read-only and destructive", name)
		}
	}
}

// A shared key would give a retired name two meanings.
func TestRemovedToolsAreDistinctFromRenames(t *testing.T) {
	renames := MCPToolRenames()
	for name, guidance := range MCPRemovedTools() {
		if _, ok := renames[name]; ok {
			t.Errorf("%s is in both MCPToolRenames and MCPRemovedTools", name)
		}
		if _, ok := mcpToolDescriptors[name]; !ok {
			t.Errorf("%s has no descriptor, so registering its removed-tool stub would panic", name)
		}
		if strings.TrimSpace(guidance) == "" {
			t.Errorf("%s has no guidance naming its replacement", name)
		}
	}
}
