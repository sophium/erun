package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sessionStartHookEntry and sessionStartGroup decode one SessionStart hook
// block from settings.json, shared by the tests below.
type sessionStartHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type sessionStartGroup struct {
	Matcher string                  `json:"matcher"`
	Hooks   []sessionStartHookEntry `json:"hooks"`
}

// readSessionStartGroups decodes the SessionStart hook blocks currently
// written to settings.json at path, returning the raw bytes too so callers can
// include them in failure messages.
func readSessionStartGroups(t *testing.T, path string) ([]sessionStartGroup, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Hooks struct {
			SessionStart []sessionStartGroup `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	return settings.Hooks.SessionStart, data
}

// sessionStartCommandCounts flattens every command across the given groups
// into a command -> occurrence-count map.
func sessionStartCommandCounts(groups []sessionStartGroup) map[string]int {
	seen := map[string]int{}
	for _, group := range groups {
		for _, hook := range group.Hooks {
			seen[hook.Command]++
		}
	}
	return seen
}

// assertSessionStartCommandsUnique fails if any SessionStart command appears
// more than once across all groups: two matcher blocks ("startup", "resume")
// carrying the same three commands would mean every SessionStart command,
// including the ~5.5KB contract injection, is installed (and so read into
// every session's context) twice.
func assertSessionStartCommandsUnique(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if count != 1 {
			t.Fatalf("SessionStart command installed %d times, want 1:\n%s\nfull settings:\n%s", count, cmd, data)
		}
	}
}

// assertSessionStartMatchersCover fails unless every source is matched by
// some group's matcher, tested the same way Claude Code itself resolves a
// SessionStart matcher: as a regex against the event's source.
func assertSessionStartMatchersCover(t *testing.T, groups []sessionStartGroup, data []byte, sources ...string) {
	t.Helper()
	for _, source := range sources {
		matched := false
		for _, group := range groups {
			re, err := regexp.Compile(group.Matcher)
			if err != nil {
				t.Fatalf("SessionStart matcher %q does not compile as a regex: %v", group.Matcher, err)
			}
			if re.MatchString(source) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("no SessionStart matcher covers source %q:\n%s", source, data)
		}
	}
}

func TestOrchestratorSessionStartHookInjectsContract(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	groups, data := readSessionStartGroups(t, filepath.Join(dir, ".claude", "settings.json"))

	contractGroups := make([]sessionStartGroup, 0, len(groups))
	for _, group := range groups {
		if len(group.Hooks) == 0 || group.Hooks[0].Type != "command" {
			t.Fatalf("SessionStart matcher %q missing a command hook:\n%s", group.Matcher, data)
		}
		if !strings.Contains(group.Hooks[0].Command, "CLAUDE.md") {
			t.Fatalf("SessionStart command does not inject the contract (print CLAUDE.md):\n%s", group.Hooks[0].Command)
		}
		contractGroups = append(contractGroups, group)
	}
	assertSessionStartCommandsUnique(t, groups, data)
	assertSessionStartMatchersCover(t, contractGroups, data, "startup", "resume", "clear", "compact")
}

// TestOrchestratorSessionStartHookPrunesEarlierDuplicatesAndPreservesForeignHooks
// seeds settings.json with the shape an earlier release left on disk -- two
// matcher blocks, each carrying the full three-command SessionStart payload --
// alongside an operator-owned SessionStart hook the installer has never seen.
// Installing on top of that must collapse the
// duplicate blocks down to one clean copy, leave the operator's own hook alone,
// and stay exactly that size across a second install (simulating a further
// desktop restart), never growing.
func TestOrchestratorSessionStartHookPrunesEarlierDuplicatesAndPreservesForeignHooks(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := orchestratorsRoot()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	staleContractCommand := orchestratorSkillHookCommand(dir)
	stale := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"matcher": "startup", "hooks": []any{
					map[string]any{"type": "command", "command": staleContractCommand},
				}},
				map[string]any{"matcher": "resume", "hooks": []any{
					map[string]any{"type": "command", "command": staleContractCommand},
				}},
				map[string]any{"matcher": "clear", "hooks": []any{
					map[string]any{"type": "command", "command": "echo operator-owned"},
				}},
			},
		},
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	groupsAfterFirst, data := readSessionStartGroups(t, settingsPath)
	assertSessionStartContractCommandPrunedToOne(t, groupsAfterFirst, data)
	if !strings.Contains(string(data), "echo operator-owned") {
		t.Fatalf("expected the operator's own SessionStart hook preserved:\n%s", data)
	}

	// A further install (another desktop restart) against the now-clean file
	// must not grow the count.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace (second run): %v", err)
	}
	groupsAfterSecond, data2 := readSessionStartGroups(t, settingsPath)
	if got, want := sessionStartTotalCommands(groupsAfterSecond), sessionStartTotalCommands(groupsAfterFirst); got != want {
		t.Fatalf("SessionStart command count changed across a second install: got %d, want %d (unchanged):\n%s", got, want, data2)
	}
}

// sessionStartTotalCommands counts every hook entry across all groups.
func sessionStartTotalCommands(groups []sessionStartGroup) int {
	n := 0
	for _, group := range groups {
		n += len(group.Hooks)
	}
	return n
}

// assertSessionStartContractCommandPrunedToOne fails if the contract-injection
// command (identified by its fallback text) appears more than once.
func assertSessionStartContractCommandPrunedToOne(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if strings.Contains(cmd, orchestratorContractFallback) && count != 1 {
			t.Fatalf("expected the earlier duplicate contract-injection blocks pruned to one, found %d:\n%s", count, data)
		}
	}
}

// runOrchestratorSessionStartGroup runs every command in the SessionStart
// group whose matcher regex matches source, the same way Claude Code itself
// dispatches a SessionStart event, and returns their combined stdout. Fails
// the test if no group's matcher covers source at all.
func runOrchestratorSessionStartGroup(t *testing.T, shell string, groups []sessionStartGroup, data []byte, orchestratorID, source string) string {
	t.Helper()
	var out strings.Builder
	matched := false
	for _, group := range groups {
		re, err := regexp.Compile(group.Matcher)
		if err != nil {
			t.Fatalf("SessionStart matcher %q does not compile as a regex: %v", group.Matcher, err)
		}
		if !re.MatchString(source) {
			continue
		}
		matched = true
		for _, hook := range group.Hooks {
			cmd := exec.Command(shell, "-c", hook.Command)
			cmd.Stdin = strings.NewReader(`{"session_id":"post-` + source + `-session","hook_event_name":"SessionStart","source":"` + source + `"}`)
			cmd.Env = append(os.Environ(), "ERUN_ORCHESTRATOR_ID="+orchestratorID)
			stdout, err := cmd.Output()
			if err != nil {
				t.Fatalf("run SessionStart hook for source %q: %v", source, err)
			}
			out.Write(stdout)
		}
	}
	if !matched {
		t.Fatalf("no SessionStart matcher covers source %q:\n%s", source, data)
	}
	return out.String()
}

// assertRoleFilePreservedOnDisk fails unless rolePath still contains marker --
// the operator's own content must never be rewritten.
func assertRoleFilePreservedOnDisk(t *testing.T, rolePath, marker string) {
	t.Helper()
	got, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read role file: %v", err)
	}
	if !strings.Contains(string(got), marker) {
		t.Fatalf("expected the role file preserved on disk, got %q", got)
	}
}

// assertSessionStartReinjectsRoleAndContract runs the installed SessionStart
// hooks for each of sources against a real shell, the way Claude Code would
// dispatch them, and fails unless every one re-injects both the role marker
// and the shared contract into stdout.
func assertSessionStartReinjectsRoleAndContract(t *testing.T, shell, orchestratorID, roleMarker string, groups []sessionStartGroup, data []byte, sources ...string) {
	t.Helper()
	for _, source := range sources {
		injected := runOrchestratorSessionStartGroup(t, shell, groups, data, orchestratorID, source)
		if !strings.Contains(injected, roleMarker) {
			t.Fatalf("source %q did not re-inject the role file into context; got stdout:\n%s", source, injected)
		}
		if !strings.Contains(injected, "# Orchestrator working directory") {
			t.Fatalf("source %q did not re-inject the shared contract into context; got stdout:\n%s", source, injected)
		}
	}
}

// TestOrchestratorSessionStartHookReinjectsRoleFileOnClearAndCompact is the
// end-to-end reproduction from #1232: an orchestrator's standing role
// (CLAUDE.<id>.md) survives on disk across a /clear or a compaction, but
// nothing re-injected it into context because SessionStart's "clear" and
// "compact" sources were not registered alongside "startup"/"resume". This
// runs the actual installed hook commands, the way Claude Code would dispatch
// them for each source, and checks the role text is really printed to stdout
// rather than merely asserting the matcher string covers the source.
func TestOrchestratorSessionStartHookReinjectsRoleFileOnClearAndCompact(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	const orchestratorID = "erun-issues"
	const roleMarker = "STANDING ROLE MARKER: triage erun issues, never close without a reproduction"

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	// Seed a non-empty, operator-authored role file BEFORE the per-id ensure
	// call, matching the real sequence (the file is written once and never
	// rewritten): the operator's own content, not the placeholder seed, is
	// what must survive and be re-injected.
	rolePath := filepath.Join(dir, "CLAUDE."+orchestratorID+".md")
	if err := os.WriteFile(rolePath, []byte(roleMarker+"\n"), 0o644); err != nil {
		t.Fatalf("seed role file: %v", err)
	}
	if _, err := app.ensureOrchestratorWorkspaceFor(orchestratorID); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor: %v", err)
	}
	assertRoleFilePreservedOnDisk(t, rolePath, roleMarker)

	groups, data := readSessionStartGroups(t, filepath.Join(dir, ".claude", "settings.json"))
	assertSessionStartReinjectsRoleAndContract(t, shell, orchestratorID, roleMarker, groups, data, "clear", "compact")
}

func TestOrchestratorSessionStartHookPreservesExistingSettings(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	claudeDir := filepath.Join(orchestratorsRoot(), ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pre-existing project settings file with an unrelated key and hook event.
	pre := `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo keep"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	if settings["model"] != "opus" {
		t.Fatalf("expected unrelated key preserved, got %v\n%s", settings["model"], data)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	// PreToolUse is an event erun writes to as well now, so "preserved" has to
	// mean the operator's own hook is still there — not merely that the key is.
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if !strings.Contains(string(data), "echo keep") {
		t.Fatalf("expected the operator's own PreToolUse hook preserved:\n%s", data)
	}
	if len(preToolUse) < 2 {
		t.Fatalf("expected erun's report merged alongside it, got %d blocks:\n%s", len(preToolUse), data)
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatalf("expected SessionStart hook added:\n%s", data)
	}
}
