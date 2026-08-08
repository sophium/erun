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

	if _, ok := readOrchestratorActivity("erun", now); ok {
		t.Fatal("an orchestrator that has reported nothing is not working")
	}

	writeOrchestratorActivity(t, "erun", orchestratorActivity{Busy: true, AtUnix: now.Unix()})
	activity, ok := readOrchestratorActivity("erun", now)
	if !ok || !activity.Busy {
		t.Fatalf("a fresh working report must be believed, got %+v %v", activity, ok)
	}

	writeOrchestratorActivity(t, "erun", orchestratorActivity{Busy: false, AtUnix: now.Unix()})
	activity, ok = readOrchestratorActivity("erun", now)
	if !ok || activity.Busy {
		t.Fatalf("the turn-end report must clear it, got %+v %v", activity, ok)
	}
}

// A session killed mid-turn never writes its end. Without a bound its row would
// spin for as long as the desktop ran — the same failure by another route.
func TestOrchestratorActivityExpiresAStaleWorkingReport(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorActivity(t, "erun", orchestratorActivity{
		Busy:   true,
		AtUnix: now.Add(-orchestratorActivityTTL - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorActivity("erun", now); ok {
		t.Fatal("a report older than its TTL must not keep a row spinning")
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
	if _, ok := readOrchestratorActivity("erun", now); ok {
		t.Fatal("unparseable input must not read as working")
	}
	if _, ok := readOrchestratorActivity("  ", now); ok {
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
