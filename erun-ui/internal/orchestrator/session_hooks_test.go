package orchestrator

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

type sessionStartHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type sessionStartGroup struct {
	Matcher string                  `json:"matcher"`
	Hooks   []sessionStartHookEntry `json:"hooks"`
}

// readSessionStartGroups also returns the raw bytes for failure messages.
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

func sessionStartCommandCounts(groups []sessionStartGroup) map[string]int {
	seen := map[string]int{}
	for _, group := range groups {
		for _, hook := range group.Hooks {
			seen[hook.Command]++
		}
	}
	return seen
}

// assertSessionStartCommandsUnique guards against the ~5.5KB contract being injected twice.
func assertSessionStartCommandsUnique(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if count != 1 {
			t.Fatalf("SessionStart command installed %d times, want 1:\n%s\nfull settings:\n%s", count, cmd, data)
		}
	}
}

// assertSessionStartMatchersCover matches each source as a regex, as Claude Code does.
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

// An earlier release left two matcher blocks with the full payload; installing
// must collapse them, keep the operator's hook, and never grow on reinstall.
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

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace (second run): %v", err)
	}
	groupsAfterSecond, data2 := readSessionStartGroups(t, settingsPath)
	if got, want := sessionStartTotalCommands(groupsAfterSecond), sessionStartTotalCommands(groupsAfterFirst); got != want {
		t.Fatalf("SessionStart command count changed across a second install: got %d, want %d (unchanged):\n%s", got, want, data2)
	}
}

func sessionStartTotalCommands(groups []sessionStartGroup) int {
	n := 0
	for _, group := range groups {
		n += len(group.Hooks)
	}
	return n
}

func assertSessionStartContractCommandPrunedToOne(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if strings.Contains(cmd, orchestratorContractFallback) && count != 1 {
			t.Fatalf("expected the earlier duplicate contract-injection blocks pruned to one, found %d:\n%s", count, data)
		}
	}
}

// runOrchestratorSessionStartGroup dispatches source as Claude Code does and returns stdout.
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

// Runs the hooks rather than only checking that the matcher covers the source.
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
	// Seed operator content before the ensure call, as in the real sequence.
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
	// erun writes PreToolUse too, so check the operator's own hook survives.
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
