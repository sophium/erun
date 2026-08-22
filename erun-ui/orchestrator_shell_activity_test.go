package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOrchestratorShellActivity(t *testing.T, id string, activity orchestratorShellActivity) {
	t.Helper()
	path := orchestratorShellActivityPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// The whole point of the report: a background shell can outlive the turn that
// started it, so the desktop must be able to say "still running" from a report
// alone, independent of whatever orchestratorActivity says about the turn.
func TestOrchestratorShellActivityIsReadFromTheReportNotTheTerminal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	if _, ok := readOrchestratorShellActivity("erun", now, true); ok {
		t.Fatal("an orchestrator that has reported nothing has no running shell")
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", AtUnix: now.Unix(),
	})
	activity, ok := readOrchestratorShellActivity("erun", now, true)
	if !ok || !activity.Running || activity.Command != "sleep 300" {
		t.Fatalf("a fresh running report must be believed, got %+v %v", activity, ok)
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{Running: false, AtUnix: now.Unix()})
	activity, ok = readOrchestratorShellActivity("erun", now, true)
	if !ok || activity.Running {
		t.Fatalf("the clear report must read as not running, got %+v %v", activity, ok)
	}
}

// The defect this whole file exists to avoid resurrecting: a report from an
// orchestrator that has genuinely turned idle in the ordinary way (its turn
// ended, no shell survives it) must not keep spinning past a long safety
// bound — hours, since a legitimate background build or poll loop can
// reasonably run that long, and the operator seeing the real elapsed time is
// the point — but it must still eventually age out if nothing ever clears it.
func TestOrchestratorShellActivitySurvivesALongRunButStillBounds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Add(-orchestratorActivityTTL - 5*time.Minute).Unix(),
	})
	if activity, ok := readOrchestratorShellActivity("erun", now, true); !ok || !activity.Running {
		t.Fatalf("a live session's shell may run well past the short bound, got %+v %v", activity, ok)
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Add(-orchestratorShellActivitySafetyBound - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorShellActivity("erun", now, true); ok {
		t.Fatal("a report nothing has renewed for hours must still age out eventually")
	}
}

// A session the desktop can no longer see is the killed-mid-run case: nothing
// will ever write its clear, so it ages out on the short bound instead of the
// generous live one.
func TestOrchestratorShellActivityExpiresQuicklyForADeadSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Add(-orchestratorActivityTTL - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorShellActivity("erun", now, false); ok {
		t.Fatal("a report from a session we can no longer see must not keep the indicator spinning")
	}
}

// Unreadable input answers "not running": the report may keep the indicator
// spinning, never invent a spin.
func TestOrchestratorShellActivityTreatsUnreadableInputAsNotRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	path := orchestratorShellActivityPath("erun")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := readOrchestratorShellActivity("erun", now, true); ok {
		t.Fatal("unparseable input must not read as running")
	}
	if _, ok := readOrchestratorShellActivity("  ", now, true); ok {
		t.Fatal("an orchestrator with no id has no report")
	}
}

// The start hook only fires for Bash and only writes when the call actually
// backgrounded a shell (its own reasoning, not asserted here); the clear hook
// only fires for TaskOutput/TaskStop. Both resolve their own orchestrator at
// run time, the same way the busy report does, since the workspace settings
// file is shared by every orchestrator.
func TestOrchestratorShellActivityHooksResolveTheirOwnOrchestrator(t *testing.T) {
	blocks := orchestratorShellActivityHookBlocks()
	if len(blocks) != 2 {
		t.Fatalf("expected a start block and a clear block, got %d: %+v", len(blocks), blocks)
	}
	start := shellHookBlockCommand(t, blocks[0], "Bash")
	clear := shellHookBlockCommand(t, blocks[1], "TaskOutput|TaskStop")

	for _, command := range []string{start, clear} {
		if !strings.Contains(command, "$ERUN_ORCHESTRATOR_ID") {
			t.Fatalf("the hook must resolve its orchestrator at run time: %q", command)
		}
		if !strings.Contains(command, `[ -n "$ERUN_ORCHESTRATOR_ID" ]`) {
			t.Fatalf("the hook must skip a session with no id: %q", command)
		}
		if !strings.Contains(command, "node -e") {
			t.Fatalf("the hook must parse structured JSON, not scan text: %q", command)
		}
	}
}

func shellHookBlockCommand(t *testing.T, block any, wantMatcher string) string {
	t.Helper()
	group, ok := block.(map[string]any)
	if !ok {
		t.Fatalf("unexpected block shape: %+v", block)
	}
	if matcher, _ := group["matcher"].(string); matcher != wantMatcher {
		t.Fatalf("expected matcher %q, got %q", wantMatcher, matcher)
	}
	hooks, ok := group["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		t.Fatalf("expected one command, got %+v", group["hooks"])
	}
	entry, ok := hooks[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected hook shape: %+v", hooks[0])
	}
	command, _ := entry["command"].(string)
	return command
}

// Installing our two blocks on an event must not delete what the operator
// already bound to it, and must stay idempotent across restarts.
func TestOrchestratorShellActivityHookMergePreservesOtherHooks(t *testing.T) {
	theirs := map[string]any{
		"matcher": "Write",
		"hooks":   []any{map[string]any{"type": "command", "command": "echo keep"}},
	}
	ours := orchestratorShellActivityHookBlocks()

	merged := mergeOrchestratorHookBlocks([]any{theirs}, ours, isOrchestratorShellActivityHookBlock)
	if len(merged) != 3 {
		t.Fatalf("expected theirs kept and both of ours appended, got %d: %+v", len(merged), merged)
	}
	again := mergeOrchestratorHookBlocks(merged, ours, isOrchestratorShellActivityHookBlock)
	if len(again) != 3 {
		t.Fatalf("re-installing must replace our own blocks, not stack them: %+v", again)
	}
	if isOrchestratorShellActivityHookBlock(again[0]) {
		t.Fatalf("the operator's hook must survive and stay theirs: %+v", again)
	}
}

// Both PostToolUse hooks — the start matcher and the clear matcher — must be
// installed, since the report needs both halves to ever clear itself promptly.
func TestOrchestratorShellActivityHooksInstallOnPostToolUseOnly(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	if !eventCarriesShellActivityReport(hooks["PostToolUse"]) {
		t.Fatalf("PostToolUse is missing the shell activity report:\n%s", data)
	}
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "Stop"} {
		if eventCarriesShellActivityReport(hooks[event]) {
			t.Fatalf("%s must not carry the shell activity report: tool_response does not exist before a call completes:\n%s", event, data)
		}
	}
}

func eventCarriesShellActivityReport(blocks []any) bool {
	for _, block := range blocks {
		if isOrchestratorShellActivityHookBlock(block) {
			return true
		}
	}
	return false
}
