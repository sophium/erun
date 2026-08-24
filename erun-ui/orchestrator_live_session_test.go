package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runOrchestratorSessionRecordHook runs the command erun actually installs
// against a POSIX shell, feeding it hook stdin the way Claude Code would.
func runOrchestratorSessionRecordHook(t *testing.T, shell, orchestratorID, input string) {
	t.Helper()
	cmd := exec.Command(shell, "-c", orchestratorSessionRecordHookCommand())
	cmd.Stdin = strings.NewReader(input)
	if orchestratorID != "" {
		cmd.Env = append(os.Environ(), "ERUN_ORCHESTRATOR_ID="+orchestratorID)
	} else {
		cmd.Env = os.Environ()
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run session-record hook: %v\n%s", err, out)
	}
}

// The hook is a shell one-liner, so asserting on its text would prove nothing
// about what it does. This runs it against real hook-shaped stdin.
func TestOrchestratorSessionRecordHookWritesTheLiveSessionID(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	runOrchestratorSessionRecordHook(t, shell, "agent-1", `{"session_id":"live-123","hook_event_name":"PostToolUse"}`)

	got, ok := readOrchestratorLiveSessionID("agent-1")
	if !ok || got != "live-123" {
		t.Fatalf("expected the live session id to be recorded, got %q ok=%v", got, ok)
	}
}

// A transient session (Investigate) carries no orchestrator id and must write
// nothing rather than a file named for nobody.
func TestOrchestratorSessionRecordHookSkipsASessionWithNoID(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	runOrchestratorSessionRecordHook(t, shell, "", `{"session_id":"live-123"}`)

	if _, err := os.Stat(orchestratorLiveSessionDir()); !os.IsNotExist(err) {
		t.Fatalf("expected no live-session dir written without an orchestrator id, stat err %v", err)
	}
}

// Unparseable hook input must not wedge or crash the hook -- same fail-open
// discipline as the no-ask guard and the activity reports.
func TestOrchestratorSessionRecordHookIgnoresUnparseableInput(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	runOrchestratorSessionRecordHook(t, shell, "agent-1", "not json at all")

	if _, ok := readOrchestratorLiveSessionID("agent-1"); ok {
		t.Fatal("expected no live session id recorded from unparseable input")
	}
}

// A later hook firing overwrites the file with whatever session_id it sees --
// the mechanism that lets the record catch up the moment Claude Code forks the
// transcript on compaction.
func TestOrchestratorSessionRecordHookOverwritesOnEachFire(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	runOrchestratorSessionRecordHook(t, shell, "agent-1", `{"session_id":"before-compact"}`)
	runOrchestratorSessionRecordHook(t, shell, "agent-1", `{"session_id":"after-compact"}`)

	got, ok := readOrchestratorLiveSessionID("agent-1")
	if !ok || got != "after-compact" {
		t.Fatalf("expected the most recent session id to win, got %q ok=%v", got, ok)
	}
}

func TestReadOrchestratorLiveSessionIDTreatsUnreadableInputAsAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	if _, ok := readOrchestratorLiveSessionID("nobody-wrote-this"); ok {
		t.Fatal("expected no live session id for an id nothing wrote")
	}

	path := orchestratorLiveSessionPath("agent-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create live session dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write malformed live session: %v", err)
	}
	if _, ok := readOrchestratorLiveSessionID("agent-1"); ok {
		t.Fatal("expected unparseable live session file to read as absent")
	}
}

// preferLiveOrchestratorSessionID must never trust a live record it cannot
// tell apart from the spawn id it already has, or verify against disk.
func TestPreferLiveOrchestratorSessionIDFallsBackAppropriately(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	if got := preferLiveOrchestratorSessionID("agent-1", "spawn-id"); got != "spawn-id" {
		t.Fatalf("no record at all: got %q, want the spawn id", got)
	}

	stageOrchestratorLiveSession(t, "agent-1", "spawn-id")
	if got := preferLiveOrchestratorSessionID("agent-1", "spawn-id"); got != "spawn-id" {
		t.Fatalf("record matches the spawn id: got %q, want the spawn id", got)
	}

	stageOrchestratorLiveSession(t, "agent-1", "forked-but-not-on-disk")
	if got := preferLiveOrchestratorSessionID("agent-1", "spawn-id"); got != "spawn-id" {
		t.Fatalf("record names a conversation absent from disk: got %q, want the spawn id", got)
	}

	stageOrchestratorConversation(t, "forked-but-not-on-disk")
	if got := preferLiveOrchestratorSessionID("agent-1", "spawn-id"); got != "forked-but-not-on-disk" {
		t.Fatalf("record names a real, different conversation: got %q, want it preferred", got)
	}
}

// The settings file must carry the recorder on every SessionStart matcher
// group and every turn-boundary event the activity reports already ride, so a
// fork is caught whichever fires next.
func TestOrchestratorSessionRecordHookIsWiredOnSessionStart(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	for _, matcherBlock := range hooks["SessionStart"] {
		block, ok := matcherBlock.(map[string]any)
		if !ok {
			t.Fatalf("unexpected SessionStart block: %+v", matcherBlock)
		}
		if !hookGroupContainsMarker(t, block, orchestratorLiveSessionHookMarker()) {
			t.Fatalf("SessionStart matcher %v missing the live-session recorder:\n%s", block["matcher"], data)
		}
	}
}

// The recorder rides every turn-boundary event the activity reports already
// use, so a compaction fork is caught whichever fires next.
func TestOrchestratorSessionRecordHookIsWiredOnEveryTurnBoundaryEvent(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"} {
		if !anyOrchestratorLiveSessionHookBlock(hooks[event]) {
			t.Fatalf("%s missing the live-session recorder:\n%s", event, data)
		}
	}
}

func anyOrchestratorLiveSessionHookBlock(events []any) bool {
	for _, block := range events {
		if isOrchestratorLiveSessionHookBlock(block) {
			return true
		}
	}
	return false
}

// hookGroupContainsMarker checks a matcher-keyed hook group (used only by
// SessionStart) for a command containing marker.
func hookGroupContainsMarker(t *testing.T, block map[string]any, marker string) bool {
	t.Helper()
	hooks, ok := block["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		if command, ok := entry["command"].(string); ok && strings.Contains(command, marker) {
			return true
		}
	}
	return false
}
