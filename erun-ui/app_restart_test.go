package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// restartTestApp is an app whose restart records live in a temp dir and whose
// relaunch/quit are inert, so a test can drive the whole hand-off without
// spawning a second desktop.
func restartTestApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	restoreDir := filepath.Join(home, "state", orchestratorRestoreDirName)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		orchestratorRestoreDir: restoreDir,
		orchestratorOpenPath:   filepath.Join(home, orchestratorOpenFileName),
		relaunchApp:            func() error { return nil },
		quitApp:                func() {},
		// beforeClose reads the window's maximised state on its way out, and
		// the real probe needs a Wails context a test does not have.
		windowMaximised:             func(context.Context) bool { return false },
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, restoreDir
}

// TestRestartAppIsNotCancelledByTheCloseGate is the reported failure at its
// cause. RestartApp spawns the successor and then asks Wails to quit, and
// Wails abandons that quit outright when OnBeforeClose returns true --
// beforeClose does, for as long as any activity is running, which is exactly
// when an operator restarts to pick up a rebuild. The predecessor then never
// exits: it keeps the control record and the control port, the successor waits
// on a process that is not going away, and the operator is left with two
// desktops, the one in front of them still the old binary.
//
// The quit is driven the way Wails drives it, through beforeClose, so what is
// asserted is the real decision rather than a stub's.
func TestRestartAppIsNotCancelledByTheCloseGate(t *testing.T) {
	app, _ := restartTestApp(t)
	app.activityQueue.start(activityQueueEntry{
		ID:      "deploy-1",
		Command: "deploy",
		Status:  activityQueueStatusRunning,
	})

	quitLanded := false
	app.deps.quitApp = func() {
		if app.beforeClose(context.Background()) {
			return
		}
		quitLanded = true
	}

	if err := app.RestartApp("agent-1"); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	if !quitLanded {
		t.Fatal("the restart's own quit was cancelled by the close gate: the predecessor keeps running while the successor waits on it, which is the two-desktop state a restart must not reach")
	}
}

// TestRestartQuitStallWatch_EndsAQuitThatNeverLanded pins the escalation the
// two-desktop state depends on being bounded and legible. A restart has already
// relaunched its successor and written its hand-off before it asks to quit, so a
// predecessor still running its whole grace later is a real second desktop
// holding the control record — and one that only logs is the reported state,
// where the operator's window is still the old binary. The wait is handed to the
// watch rather than slept through, and the exit is substituted rather than
// taken, so the escalation is witnessed without ending the test process.
func TestRestartQuitStallWatch_EndsAQuitThatNeverLanded(t *testing.T) {
	var waited time.Duration
	exits := 0
	var reason string
	restartQuitStallWatch(restartQuitStallGrace, func(d time.Duration) { waited += d }, func(r string) {
		exits++
		reason = r
	}, "test reason")

	if waited != restartQuitStallGrace {
		t.Fatalf("waited %s, want the quit given exactly the bounded grace %s", waited, restartQuitStallGrace)
	}
	if exits != 1 {
		t.Fatalf("exited %d times, want the stalled quit to end the process exactly once", exits)
	}
	if reason != "test reason" {
		t.Fatalf("exit reason = %q, want the reason it was handed, so the escalation is recorded rather than a desktop that vanished", reason)
	}
}

// TestQuitDesktopApp_ReportsWhenThereWasNoWindowToAsk pins the gate the stall
// watch is armed behind. A headless or not-yet-started app has no Wails context
// to quit, so no quit was ever asked of it and "still running" means nothing:
// arming the escalation there would end a healthy process that was never asked
// to go anywhere.
func TestQuitDesktopApp_ReportsWhenThereWasNoWindowToAsk(t *testing.T) {
	app := &App{}
	if app.quitDesktopApp() {
		t.Fatal("quitDesktopApp reported a quit for an app with no Wails context to ask")
	}
}

// readRestoreState reads the hand-off staged in one orchestrator's own slot,
// asserting the persisted file rather than what the resolver reports about it.
func readRestoreState(t *testing.T, dir, orchestratorID string) orchestratorRestoreState {
	t.Helper()
	data, err := os.ReadFile(orchestratorRestorePath(dir, orchestratorID))
	if err != nil {
		t.Fatalf("read restore file for %s: %v", orchestratorID, err)
	}
	var state orchestratorRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode restore file for %s: %v", orchestratorID, err)
	}
	return state
}

func TestRestartAppPersistsTargetRelaunchesAndQuits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	relaunched, quit := false, false
	app := NewApp(erunUIDeps{
		store:                  newOrchestratorStubStore(t.TempDir()),
		orchestratorRestoreDir: filepath.Join(home, "state", orchestratorRestoreDirName),
		orchestratorOpenPath:   filepath.Join(home, "orchestrator-open.json"),
		relaunchApp:            func() error { relaunched = true; return nil },
		quitApp:                func() { quit = true },
	})
	defer app.shutdown(context.Background())

	if err := app.RestartApp("agent-1"); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	if !relaunched || !quit {
		t.Fatalf("expected relaunch and quit to fire, got relaunched=%v quit=%v", relaunched, quit)
	}
	// The hand-off is honored once, then cleared. Nothing was open here, so with
	// it gone there is no durable record to fall back to either.
	if got := app.ResolveOrchestratorToReopen(); got.OrchestratorID != "agent-1" {
		t.Fatalf("expected restore target agent-1, got %q", got.OrchestratorID)
	}
	if got := app.ResolveOrchestratorToReopen(); got.OrchestratorID != "" {
		t.Fatalf("expected the restart hand-off to be consumed exactly once, got %q", got.OrchestratorID)
	}
}

// A restart does not interrupt an orchestrator's background agent, so the
// hand-off names the pane owner and nothing else: no conversation to resume and
// no prompt to send into a turn that never stopped.
func TestRestartHandOffNamesOnlyThePaneOwner(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	data, err := os.ReadFile(orchestratorRestorePath(restoreDir, id))
	if err != nil {
		t.Fatalf("read hand-off: %v", err)
	}
	for _, field := range []string{"conversationId", "resumePrompt", "environments"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("expected the hand-off to carry no %s, got %s", field, data)
		}
	}
	if state := readRestoreState(t, restoreDir, id); state.OrchestratorID != id {
		t.Fatalf("expected the hand-off to name %s, got %+v", id, state)
	}
}

// Every orchestrator that was open comes back: the hand-off decides which one
// owns the pane, and the rest are reopened beside it in recorded order.
func TestReopenGivesThePaneToTheHandOffAndReopensTheRest(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := recordOpenOrchestrator(app.deps.orchestratorOpenPath, id); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}
	if err := writeOrchestratorRestoreTarget(restoreDir, orchestratorRestoreState{OrchestratorID: "beta"}, time.Now()); err != nil {
		t.Fatalf("write hand-off: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != "beta" {
		t.Fatalf("expected the hand-off's orchestrator to own the pane, got %+v", target)
	}
	if len(target.AlsoReopen) != 2 || target.AlsoReopen[0].OrchestratorID != "alpha" || target.AlsoReopen[1].OrchestratorID != "gamma" {
		t.Fatalf("expected alpha and gamma reopened beside it, got %+v", target.AlsoReopen)
	}
}

// With no hand-off, the most recently (re)started orchestrator owns the pane.
func TestReopenWithoutAHandOffGivesThePaneToTheMostRecent(t *testing.T) {
	app, _ := restartTestApp(t)
	for _, id := range []string{"alpha", "beta"} {
		if err := recordOpenOrchestrator(app.deps.orchestratorOpenPath, id); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}
	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != "beta" || len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != "alpha" {
		t.Fatalf("expected beta to own the pane with alpha beside it, got %+v", target)
	}
}

// Which of several pending hand-offs a launch acts on is the most recent one —
// the restart the operator triggered last — and every one of them is consumed.
func TestTheMostRecentHandOffIsTheOneDelivered(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	for _, staged := range []struct {
		id  string
		age time.Duration
	}{{"zeta", 3 * time.Minute}, {"alpha", time.Minute}, {"mid", 2 * time.Minute}} {
		if err := writeOrchestratorRestoreTarget(dir, orchestratorRestoreState{OrchestratorID: staged.id}, now.Add(-staged.age)); err != nil {
			t.Fatalf("write hand-off for %s: %v", staged.id, err)
		}
	}

	if delivered := consumeOrchestratorRestoreTarget(dir, now); delivered.OrchestratorID != "alpha" {
		t.Fatalf("expected the newest hand-off to be delivered, got %q", delivered.OrchestratorID)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("expected every hand-off consumed, got %v (err %v)", entries, err)
	}
}

// The upgrade arrives through a restart the previous release staged, in its
// own shape and possibly in the single shared slot, so that hand-off is still
// read once; the fields it carried for a resume are ignored.
func TestHandOffLeftByAnEarlierReleaseIsStillDeliveredOnce(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	legacy := legacyOrchestratorRestorePath(restoreDir)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	data := `{"orchestratorId":"erun","conversationId":"3bde477f-6a1c-4a2e-9f0d-2c5b8e7a1d34",` +
		`"environments":["frs/dev"],"resumePrompt":"finish the task","savedAtUnix":` + strconv.FormatInt(time.Now().Unix(), 10) + `}`
	if err := os.WriteFile(legacy, []byte(data), 0o644); err != nil {
		t.Fatalf("write legacy hand-off: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != "erun" {
		t.Fatalf("expected the earlier release's hand-off to be delivered, got %+v", target)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("expected the shared slot to be cleared, stat err %v", err)
	}
}

// An id is a slug by construction; a value that would resolve anywhere but its
// own slot writes nothing rather than landing outside the restore directory.
func TestRestoreSlotRejectsAnIDThatIsNotAPlainFileName(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "..", "../escape", "nested/agent"} {
		if path := orchestratorRestorePath(dir, id); path != "" {
			t.Fatalf("expected %q to resolve to no slot, got %q", id, path)
		}
		if err := writeOrchestratorRestoreTarget(dir, orchestratorRestoreState{OrchestratorID: id}, time.Now()); err != nil {
			t.Fatalf("write with id %q: %v", id, err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("expected nothing written, got %v (err %v)", entries, err)
	}
}

func TestConsumeOrchestratorRestoreTargetIgnoresStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)

	stale := orchestratorRestoreState{OrchestratorID: "agent-x"}
	if err := writeOrchestratorRestoreTarget(dir, stale, now.Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restore target: %v", err)
	}
	if got := consumeOrchestratorRestoreTarget(dir, now); got.OrchestratorID != "" {
		t.Fatalf("expected a stale target to be ignored, got %+v", got)
	}

	fresh := orchestratorRestoreState{OrchestratorID: "agent-y"}
	if err := writeOrchestratorRestoreTarget(dir, fresh, now); err != nil {
		t.Fatalf("write fresh restore target: %v", err)
	}
	if got := consumeOrchestratorRestoreTarget(dir, now); got.OrchestratorID != "agent-y" {
		t.Fatalf("expected fresh target agent-y, got %+v", got)
	}
}
