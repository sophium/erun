package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOrchestratorActivity(t *testing.T, id string, activity orchestratorActivity) {
	t.Helper()
	path := orchestratorActivityPath(id)
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

// The whole point of the report: an orchestrator's row must be able to stop
// spinning while its terminal is still talking. The old output-driven latch
// could not, because an agent TUI repaints forever.
func TestOrchestratorActivityIsReadFromTheReportNotTheTerminal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	if _, ok := readOrchestratorActivity("erun", now, true); ok {
		t.Fatal("an orchestrator that has reported nothing is not working")
	}

	writeOrchestratorActivity(t, "erun", orchestratorActivity{Busy: true, AtUnix: now.Unix()})
	activity, ok := readOrchestratorActivity("erun", now, true)
	if !ok || !activity.Busy {
		t.Fatalf("a fresh working report must be believed, got %+v %v", activity, ok)
	}

	writeOrchestratorActivity(t, "erun", orchestratorActivity{Busy: false, AtUnix: now.Unix()})
	activity, ok = readOrchestratorActivity("erun", now, true)
	if !ok || activity.Busy {
		t.Fatalf("the turn-end report must clear it, got %+v %v", activity, ok)
	}
}

// A session killed mid-turn never writes its end. Without a bound its row would
// spin for as long as the desktop ran — the same failure by another route. The
// desktop can no longer see that session, which is what puts it on the short
// bound.
func TestOrchestratorActivityExpiresAStaleWorkingReport(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorActivity(t, "erun", orchestratorActivity{
		Busy:   true,
		AtUnix: now.Add(-orchestratorActivityTTL - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorActivity("erun", now, false); ok {
		t.Fatal("a report from a session we can no longer see must not keep a row spinning")
	}
}

// Unreadable input answers "not working": the report may keep a row spinning,
// never invent a spin.
func TestOrchestratorActivityTreatsUnreadableInputAsIdle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	path := orchestratorActivityPath("erun")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := readOrchestratorActivity("erun", now, true); ok {
		t.Fatal("unparseable input must not read as working")
	}
	if _, ok := readOrchestratorActivity("  ", now, true); ok {
		t.Fatal("an orchestrator with no id has no report")
	}
}

// The orchestrators workspace is shared, so one settings file serves every
// orchestrator. A hook with an id baked in would have them all reporting as
// whichever wrote last, so it resolves its own id at run time.
func TestOrchestratorActivityHooksResolveTheirOwnOrchestrator(t *testing.T) {
	busyHook, idleHook := orchestratorActivityHooks()
	busy := hookCommandText(t, busyHook)
	idle := hookCommandText(t, idleHook)

	for _, command := range []string{busy, idle} {
		if !strings.Contains(command, "$ERUN_ORCHESTRATOR_ID") {
			t.Fatalf("the hook must resolve its orchestrator at run time: %q", command)
		}
		// A transient session carries no id and must write nothing rather than a
		// file named for nobody.
		if !strings.Contains(command, `[ -n "$ERUN_ORCHESTRATOR_ID" ]`) {
			t.Fatalf("the hook must skip a session with no id: %q", command)
		}
	}
	if !strings.Contains(busy, `"busy":true`) {
		t.Fatalf("the turn-start hook must report working: %q", busy)
	}
	if !strings.Contains(idle, `"busy":false`) {
		t.Fatalf("the turn-end hook must report idle: %q", idle)
	}
}

// An orchestrator must not drive the output-derived latch: that is the signal
// that could never clear for a repainting TUI.
func TestOrchestratorSessionsDoNotDriveTheOutputLatch(t *testing.T) {
	if aiActivityKind(sessionKindOrchestrator) {
		t.Fatal("an orchestrator's spinner must come from its own report, not from terminal output")
	}
	if !aiActivityKind(sessionKindAI) {
		t.Fatal("an env's AI tab still drives the output latch, released by the pod heartbeat")
	}
}

func hookCommandText(t *testing.T, hook []any) string {
	t.Helper()
	if len(hook) != 1 {
		t.Fatalf("expected one matcher block, got %+v", hook)
	}
	block, ok := hook[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected hook shape: %+v", hook[0])
	}
	commands, ok := block["hooks"].([]any)
	if !ok || len(commands) != 1 {
		t.Fatalf("expected one command, got %+v", block["hooks"])
	}
	command, ok := commands[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected command shape: %+v", commands[0])
	}
	text, _ := command["command"].(string)
	return text
}

// The bug this pair exists for: a turn is as long as the work is, and the report
// is written once at its start. Ageing a live session's report out on the short
// bound answered "not working" for an orchestrator that was still working — the
// most ordinary case there is.
func TestOrchestratorActivitySurvivesATurnLongerThanTheShortBound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorActivity(t, "erun", orchestratorActivity{
		Busy:   true,
		AtUnix: now.Add(-orchestratorActivityTTL - 5*time.Minute).Unix(),
	})

	activity, ok := readOrchestratorActivity("erun", now, true)
	if !ok || !activity.Busy {
		t.Fatalf("a live session's turn may outlast the short bound, got %+v %v", activity, ok)
	}

	// The same report, from a session the desktop can no longer see, is the
	// killed-mid-turn case and must not keep the row spinning.
	if _, ok := readOrchestratorActivity("erun", now, false); ok {
		t.Fatal("a session we can no longer see must not keep its working report")
	}
}

// The live bound is long, not absent. A report that stops being written at all
// still ages out, so reporting that has died cannot pin a row spinning forever.
func TestOrchestratorActivityStillBoundsALiveSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorActivity(t, "erun", orchestratorActivity{
		Busy:   true,
		AtUnix: now.Add(-orchestratorActivityLiveTTL - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorActivity("erun", now, true); ok {
		t.Fatal("a live session's report must still age out once nothing renews it")
	}
}

// A turn renews its report from the thing a working orchestrator does
// constantly. Without this the busy report exists only at the turn's first
// instant, which is the whole defect.
// orchestratorSettingsHooks composes the workspace settings the desktop writes
// and returns its hook events, so a test can assert about one thing at a time.
func orchestratorSettingsHooks(t *testing.T) (map[string][]any, []byte) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", root)

	if err := ensureOrchestratorSessionStartHook(root); err != nil {
		t.Fatalf("ensureOrchestratorSessionStartHook: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	return settings.Hooks, data
}

// eventCarriesActivityReport reports whether one hook event carries ours.
func eventCarriesActivityReport(blocks []any) bool {
	for _, block := range blocks {
		if isOrchestratorActivityHookBlock(block) {
			return true
		}
	}
	return false
}

// eventRunsCommand reports whether one hook event runs a specific command, which
// is how the idle report is recognised among the hooks bound to SessionStart.
func eventRunsCommand(blocks []any, command string) bool {
	for _, group := range blocks {
		block, _ := group.(map[string]any)
		entries, _ := block["hooks"].([]any)
		for _, entry := range entries {
			hook, _ := entry.(map[string]any)
			if got, _ := hook["command"].(string); got == command {
				return true
			}
		}
	}
	return false
}

// A turn renews its report from the thing a working orchestrator does
// constantly. Without this the busy report exists only at the turn's first
// instant, which is the whole defect.
func TestOrchestratorActivityRenewsFromToolCalls(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		if !eventCarriesActivityReport(hooks[event]) {
			t.Fatalf("%s is missing the busy report, so a long turn stops reporting:\n%s", event, data)
		}
	}
}

// SessionStart clears an inherited "working": a session killed mid-turn never
// wrote its end, and the next one must not arrive already spinning.
func TestOrchestratorActivityClearsAnInheritedWorkingReport(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	if !eventRunsCommand(hooks["SessionStart"], orchestratorActivityHookCommand(false)) {
		t.Fatalf("SessionStart must clear an inherited working report:\n%s", data)
	}
}

// Installing our report on an event must not delete what the operator already
// bound to it, and must stay idempotent across restarts.
func TestOrchestratorActivityHookMergePreservesOtherHooks(t *testing.T) {
	theirs := map[string]any{
		"matcher": "Bash",
		"hooks":   []any{map[string]any{"type": "command", "command": "echo keep"}},
	}
	busy, _ := orchestratorActivityHooks()

	merged := mergeOrchestratorActivityHook([]any{theirs}, busy)
	if len(merged) != 2 {
		t.Fatalf("expected theirs kept and ours appended, got %d: %+v", len(merged), merged)
	}
	again := mergeOrchestratorActivityHook(merged, busy)
	if len(again) != 2 {
		t.Fatalf("re-installing must replace our own block, not stack it: %+v", again)
	}
	if !isOrchestratorActivityHookBlock(again[1]) || isOrchestratorActivityHookBlock(again[0]) {
		t.Fatalf("the operator's hook must survive and stay theirs: %+v", again)
	}
}
