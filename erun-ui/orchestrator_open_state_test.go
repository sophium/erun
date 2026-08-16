package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// openStateTestApp is orchestratorTestApp with the durable open record and the
// per-orchestrator hand-off slots pinned to a temp dir, so a test can assert on
// each half of the split independently.
func openStateTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	state := t.TempDir()
	openPath := filepath.Join(state, orchestratorOpenFileName)
	restoreDir := filepath.Join(state, orchestratorRestoreDirName)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		orchestratorOpenPath:   openPath,
		orchestratorRestoreDir: restoreDir,
	})
	app.investigations.reportDir = t.TempDir()
	return app, openPath, restoreDir
}

func createAndStartOrchestrator(t *testing.T, app *App) string {
	t.Helper()
	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	return created.ID
}

// The defect this fixes: a plain quit-and-relaunch (no restart hand-off, so no
// restore file at all) came back with nothing open. Starting an orchestrator is
// what records it, so the next launch has something to reopen — and it reopens
// with no prompt, so the conversation resumes idle rather than re-running a task.
func TestPlainLaunchReopensTheOrchestratorThatWasOpen(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected a plain launch to reopen %q, got %q", id, target.OrchestratorID)
	}
	if target.ResumePrompt != "" {
		t.Fatalf("expected no prompt to auto-run on a plain launch, got %q", target.ResumePrompt)
	}
	// Durable, not one-shot: every later launch reopens it too, and it survives a
	// desktop that never got to run a shutdown hook.
	if again := app.ResolveOrchestratorToReopen(); again.OrchestratorID != id {
		t.Fatalf("expected the record to survive being read, got %q", again.OrchestratorID)
	}
	if got := readOpenOrchestrator(openPath); got != id {
		t.Fatalf("expected %q on disk, got %q", id, got)
	}
}

// Stopping is the operator saying the orchestrator should not come back.
func TestExplicitlyStoppedOrchestratorStaysClosed(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	if err := app.StopOrchestrator(id); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != "" {
		t.Fatalf("expected a stopped orchestrator to stay closed, got %q", target.OrchestratorID)
	}
}

// Deleting the definition takes the record with it, so a launch never tries to
// reopen an orchestrator that no longer exists.
func TestDeletedOrchestratorIsNotReopened(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	if err := app.DeleteOrchestrator(id); err != nil {
		t.Fatalf("DeleteOrchestrator failed: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != "" {
		t.Fatalf("expected a deleted orchestrator not to be reopened, got %q", target.OrchestratorID)
	}
}

// A restart re-records what it just respawned, so recycling a stuck agent does
// not read as the operator closing it.
func TestRestartKeepsTheOrchestratorRecordedAsOpen(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	if _, err := app.RestartOrchestrator(id, 80, 24); err != nil {
		t.Fatalf("RestartOrchestrator failed: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != id {
		t.Fatalf("expected %q to still be recorded as open after a restart, got %q", id, target.OrchestratorID)
	}
}

// Stopping one orchestrator must not close the one that owns the pane.
func TestStoppingAnotherOrchestratorLeavesTheOpenOneRecorded(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	if err := app.StopOrchestrator("some-other-orchestrator"); err == nil {
		t.Fatal("expected stopping an unknown orchestrator to error")
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != id {
		t.Fatalf("expected %q to stay recorded as open, got %q", id, target.OrchestratorID)
	}
}

// The Investigate flow spawns a session with no persisted definition, so there is
// nothing for a later launch to reopen.
func TestTransientOrchestratorIsNotRecordedAsOpen(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24); err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != "" {
		t.Fatalf("expected a transient orchestrator not to be recorded, got %q", target.OrchestratorID)
	}
}

// The restart hand-off keeps its own semantics on top of the durable record: it
// wins while it is fresh, carries the prompt that makes a rebuild+restart
// continue its task, fires exactly once, and expires — after which the durable
// record still reopens the orchestrator, just idle at its prompt.
func TestRestartHandOffStaysOneShotAndAgeBoundedOverTheDurableRecord(t *testing.T) {
	app, openPath, restoreDir := openStateTestApp(t)
	defer app.shutdown(context.Background())

	const prompt = "verify the rebuild is live, then finish the task"
	id := createAndStartOrchestrator(t, app)
	conversationID := orchestratorSessionID(id)
	stageOrchestratorConversation(t, conversationID)
	handOff := orchestratorRestoreState{
		OrchestratorID: id,
		ConversationID: conversationID,
		Environments:   []string{"frs/dev"},
		ResumePrompt:   prompt,
	}
	if err := recordOpenOrchestrator(openPath, id); err != nil {
		t.Fatalf("record open orchestrator: %v", err)
	}
	if err := writeOrchestratorRestoreTarget(restoreDir, handOff, time.Now()); err != nil {
		t.Fatalf("write restart hand-off: %v", err)
	}

	first := app.ResolveOrchestratorToReopen()
	if first.OrchestratorID != id || first.ResumePrompt != prompt {
		t.Fatalf("expected the restart hand-off to win with its prompt, got %+v", first)
	}
	// One-shot: the next launch falls back to the durable record, which runs nothing.
	second := app.ResolveOrchestratorToReopen()
	if second.OrchestratorID != id || second.ResumePrompt != "" {
		t.Fatalf("expected the hand-off to fire once and the durable record to answer idle, got %+v", second)
	}

	// Age-bounded: a hand-off older than the bound is discarded, and the durable
	// record still reopens the orchestrator without auto-running the prompt.
	if err := writeOrchestratorRestoreTarget(restoreDir, handOff, time.Now().Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restart hand-off: %v", err)
	}
	stale := app.ResolveOrchestratorToReopen()
	if stale.OrchestratorID != id || stale.ResumePrompt != "" {
		t.Fatalf("expected a stale hand-off to be dropped and the durable record to answer, got %+v", stale)
	}
}
