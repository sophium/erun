package eruncommon

import (
	"strings"
	"testing"
)

// TestToolNameEqualsItsCLIPath is the invariant #1186 exists to restore. Before
// it, five tools broke the rule silently -- `erun exec raw` surfaced as `raw`,
// `erun sshd sync` as `workspace_sync` -- and nothing would have caught a sixth.
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

// TestMCPOnlyToolsAreTheKnownSet pins the tools with no erun command behind
// them. #1186 assumed there were none beyond the five renames; there are
// eleven now (ten wire-level primitives the CLI deliberately expresses
// differently for a human, plus exec_agent, whose capability the CLI already
// covers via `erun exec job start --agent`). Pinning the set makes adding a
// twelfth a decision rather than an accident.
func TestMCPOnlyToolsAreTheKnownSet(t *testing.T) {
	want := map[string]struct{}{
		"activity_lease_list":          {},
		"activity_lease_take":          {},
		"activity_lease_release":       {},
		"idle_stop_history":            {},
		"idle_stop_record":             {},
		"idle_stop_cancel":             {},
		"cloud_list":                   {},
		"cloud_inject_aws_credentials": {},
		"cloud_clear_aws_credentials":  {},
		"terraform":                    {},
		"exec_agent":                   {},
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

// TestMCPReadOnlyToolsAreSemanticallyReadOnly holds the one direction that must
// be true between the two tables. They are separate on purpose: the capability
// allowlist is stricter (anything absent requires admin), so platform_env_list
// is read-only in meaning while still requiring admin. But a tool a read-only
// TOKEN may call must never be one that modifies its environment.
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

// TestRenamesResolveToRealTools: an alias that points at nothing is worse than
// no alias, and a rename key that is still a live tool name would shadow it.
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

// TestRetiredNameAuthorizesAsItsReplacement: the diff -> exec_diff rename moved
// an entry in the read-only allowlist. If a retired name did not resolve first,
// the rename would silently promote a read-only tool to requiring admin and a
// read-only caller would lose it without any error saying why.
func TestRetiredNameAuthorizesAsItsReplacement(t *testing.T) {
	if got := MCPToolCapability("diff"); got != MCPCapabilityRead {
		t.Errorf("MCPToolCapability(\"diff\") = %q, want %q -- the retired name must authorize as exec_diff does", got, MCPCapabilityRead)
	}
	if got := MCPToolCapability("exec_diff"); got != MCPCapabilityRead {
		t.Errorf("MCPToolCapability(\"exec_diff\") = %q, want %q", got, MCPCapabilityRead)
	}
}

// TestEveryToolDeclaresATitle: a client renders `title` in preference to `name`,
// so an empty one puts "platform_context_create" in front of a person.
func TestEveryToolDeclaresATitle(t *testing.T) {
	for _, name := range MCPToolNames() {
		descriptor, _ := MCPToolDescriptorFor(name)
		if strings.TrimSpace(descriptor.Title) == "" {
			t.Errorf("%s declares no title", name)
		}
	}
}

// TestReadOnlyToolsAreNotAlsoDestructive: the two are contradictory, and the
// spec says destructiveHint is meaningful only when readOnlyHint is false.
func TestReadOnlyToolsAreNotAlsoDestructive(t *testing.T) {
	for _, name := range MCPToolNames() {
		descriptor, _ := MCPToolDescriptorFor(name)
		if descriptor.ReadOnly && descriptor.Destructive {
			t.Errorf("%s claims to be both read-only and destructive", name)
		}
	}
}
