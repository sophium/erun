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

	if _, ok := readOrchestratorShellActivity("erun", now, true, ""); ok {
		t.Fatal("an orchestrator that has reported nothing has no running shell")
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", AtUnix: now.Unix(),
	})
	activity, ok := readOrchestratorShellActivity("erun", now, true, "")
	if !ok || !activity.Running || activity.Command != "sleep 300" {
		t.Fatalf("a fresh running report must be believed, got %+v %v", activity, ok)
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{Running: false, AtUnix: now.Unix()})
	activity, ok = readOrchestratorShellActivity("erun", now, true, "")
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
	if activity, ok := readOrchestratorShellActivity("erun", now, true, ""); !ok || !activity.Running {
		t.Fatalf("a live session's shell may run well past the short bound, got %+v %v", activity, ok)
	}

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Add(-orchestratorShellActivitySafetyBound - time.Minute).Unix(),
	})
	if _, ok := readOrchestratorShellActivity("erun", now, true, ""); ok {
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
	if _, ok := readOrchestratorShellActivity("erun", now, false, ""); ok {
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
	if _, ok := readOrchestratorShellActivity("erun", now, true, ""); ok {
		t.Fatal("unparseable input must not read as running")
	}
	if _, ok := readOrchestratorShellActivity("  ", now, true, ""); ok {
		t.Fatal("an orchestrator with no id has no report")
	}
}

// The defect in #1274: an orchestrator id is reused across restarts, and a
// report a since-replaced session left behind must not borrow the id's
// current liveness. Requiring the report's own session id to match the one
// the desktop currently has recorded as live is what stops that borrowing —
// independent of, and in addition to, the SessionStart reset that clears the
// report at the boundary itself.
func TestOrchestratorShellActivityRejectsAReportFromAReplacedSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", SessionID: "old-session", AtUnix: now.Unix(),
	})

	if _, ok := readOrchestratorShellActivity("erun", now, true, "new-session"); ok {
		t.Fatal("a report naming a session that is no longer live must not be honoured")
	}

	activity, ok := readOrchestratorShellActivity("erun", now, true, "old-session")
	if !ok || !activity.Running {
		t.Fatalf("a report naming the current live session must still be honoured, got %+v %v", activity, ok)
	}
}

// A report with no session id (an older write, or a hook stdin that carried
// none) and a desktop with no live-session record yet must not be rejected
// just because there is nothing to compare — that would make every report
// unusable before the live-session recorder has ever fired.
func TestOrchestratorShellActivityHonoursAReportWithNoSessionIDToCompare(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	writeOrchestratorShellActivity(t, "erun", orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Unix(),
	})
	if _, ok := readOrchestratorShellActivity("erun", now, true, "new-session"); !ok {
		t.Fatal("a report with no recorded session id must not be rejected on that basis alone")
	}
	if _, ok := readOrchestratorShellActivity("erun", now, true, ""); !ok {
		t.Fatal("no live-session record at all must not be treated as a mismatch")
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
		if !strings.Contains(command, "process.env.ERUN_ORCHESTRATOR_ID") {
			t.Fatalf("the hook must resolve its orchestrator at run time: %q", command)
		}
		if !strings.Contains(command, "if(!id)return") {
			t.Fatalf("the hook must skip a session with no id: %q", command)
		}
		if !strings.Contains(command, "node -e") {
			t.Fatalf("the hook must parse structured JSON, not scan text: %q", command)
		}
		if !orchestratorHookCommandIsPortable(command) {
			t.Fatalf("the hook must run through node, not a POSIX shell: %q", command)
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

// SessionStart clears an inherited "running" shell report, the same way it
// already clears the busy report: an orchestrator id is mutable and reusable,
// so a report a previous run under this id left behind must not survive into
// a new one that hasn't started any shell of its own yet.
func TestOrchestratorShellActivityClearsAnInheritedRunningReportOnSessionStart(t *testing.T) {
	hooks, data := orchestratorSettingsHooks(t)

	if !eventRunsCommand(hooks["SessionStart"], orchestratorShellActivityResetHookCommand()) {
		t.Fatalf("SessionStart must clear an inherited running shell report:\n%s", data)
	}
}

// The start hook must capture the writing session's own id, not just the
// task id and command, so a later read can tell a report apart from one a
// since-replaced session left behind.
func TestOrchestratorShellActivityStartHookCapturesTheSessionID(t *testing.T) {
	if !strings.Contains(orchestratorShellActivityStartHookCommand(), "session_id") {
		t.Fatalf("the start hook must record the writing session's id: %q", orchestratorShellActivityStartHookCommand())
	}
}

// erun#2144: a transient orchestrator id (investigate-<nanos>, report-bug-
// <nanos>) is minted once and never reused, so once its session ends nothing
// will ever call readOrchestratorShellActivity for it again —
// reconcileOrchestratorActivity only visits ids currently in a.orchestrators.
// A "running: true" report such a session never got to clear would otherwise
// sit on disk asserting that forever. This is also exactly the ungraceful
// case: the session was killed rather than cleanly stopped, so no exit hook
// ever ran for it either, and a.orchestrators never held this id in the first
// place in this test — the sweep must not depend on either happening.
func TestReconcileOrphanedOrchestratorShellActivityReapsAnEphemeralIDNoOneWillEverAskAbout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	id := "report-bug-1788508309225841000"

	writeOrchestratorShellActivity(t, id, orchestratorShellActivity{
		Running: true, Command: "nc -z -w 4 host 22", TaskID: "task-1",
		AtUnix: now.Add(-orchestratorActivityTTL - time.Minute).Unix(),
	})

	a := &App{}
	a.reconcileOrphanedOrchestratorShellActivity(now)

	if _, ok := readOrchestratorShellActivity(id, now, false, ""); ok {
		t.Fatal("an orphaned running:true report must not survive the sweep")
	}
	if _, err := os.Stat(orchestratorShellActivityPath(id)); !os.IsNotExist(err) {
		t.Fatalf("the orphaned report file itself must be removed, got err=%v", err)
	}
}

// A record too young to be implausible must survive: the sweep must not race
// ahead of a session that simply hasn't written its next report yet.
func TestReconcileOrphanedOrchestratorShellActivityLeavesAFreshOrphanAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	id := "report-bug-1788508309225841000"

	writeOrchestratorShellActivity(t, id, orchestratorShellActivity{
		Running: true, TaskID: "task-1", AtUnix: now.Unix(),
	})

	a := &App{}
	a.reconcileOrphanedOrchestratorShellActivity(now)

	if _, err := os.Stat(orchestratorShellActivityPath(id)); err != nil {
		t.Fatalf("a fresh report must not be reaped: %v", err)
	}
}

// A session the desktop is still genuinely holding open gets the same
// generous safety bound the read path already applies — the sweep must not
// reap a legitimate long-running background shell out from under a live
// orchestrator just because it also happens to be transient.
func TestReconcileOrphanedOrchestratorShellActivityRespectsALiveSessionsSafetyBound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	id := "investigate-1788508309225841000"

	writeOrchestratorShellActivity(t, id, orchestratorShellActivity{
		Running: true, TaskID: "task-1",
		AtUnix: now.Add(-orchestratorActivityTTL - time.Minute).Unix(),
	})

	a := &App{
		orchestrators: map[string]*orchestratorSession{id: {id: id, transient: true}},
		sessions:      map[string]*managedTerminal{orchestratorSessionKey(id): {session: newStubTerminalSession()}},
	}
	a.reconcileOrphanedOrchestratorShellActivity(now)
	if _, err := os.Stat(orchestratorShellActivityPath(id)); err != nil {
		t.Fatalf("a live session's shell may run well past the short bound: %v", err)
	}

	writeOrchestratorShellActivity(t, id, orchestratorShellActivity{
		Running: true, TaskID: "task-1",
		AtUnix: now.Add(-orchestratorShellActivitySafetyBound - time.Minute).Unix(),
	})
	a.reconcileOrphanedOrchestratorShellActivity(now)
	if _, err := os.Stat(orchestratorShellActivityPath(id)); !os.IsNotExist(err) {
		t.Fatalf("even a live session's report must eventually age out, got err=%v", err)
	}
}
