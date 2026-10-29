package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/command/orchestrator"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

func openStateTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	state := t.TempDir()
	openPath := filepath.Join(state, "orchestrator-open.json")
	restoreDir := filepath.Join(state, orchestratorRestoreDirName)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		orchestratorOpenPath:        openPath,
		orchestratorRestoreDir:      restoreDir,
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
		investigationReportDir:      t.TempDir(),
	})
	return app, openPath, restoreDir
}

func createAndStartOrchestrator(t *testing.T, app *App) string {
	t.Helper()
	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	return created.ID
}

func TestPlainLaunchReopensTheOrchestratorThatWasOpen(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected a plain launch to reopen %q, got %q", id, target.OrchestratorID)
	}
	// Durable, not one-shot.
	if again := app.ResolveOrchestratorToReopen(); again.OrchestratorID != id {
		t.Fatalf("expected the record to survive being read, got %q", again.OrchestratorID)
	}
	if got := orchestratorsvc.ReadOpenOrchestrators(openPath); len(got) != 1 || got[0].OrchestratorID != id {
		t.Fatalf("expected [%q] on disk, got %v", id, got)
	}
}

func createAndStartNamedOrchestrator(t *testing.T, app *App, name, environment string) string {
	t.Helper()
	created, err := app.CreateOrchestrator(name, []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: environment}}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	return created.ID
}

func TestTwoOpenOrchestratorsAreBothRestoredOnPlainLaunch(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != second {
		t.Fatalf("expected %q (started last) to own the pane, got %q", second, target.OrchestratorID)
	}
	if len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != first {
		t.Fatalf("expected %q to also be reopened, got %v", first, target.AlsoReopen)
	}
	if got := orchestratorsvc.ReadOpenOrchestrators(openPath); len(got) != 2 || got[0].OrchestratorID != first || got[1].OrchestratorID != second {
		t.Fatalf("expected both ids recorded oldest-first, got %v", got)
	}
}

func TestStoppingOneOrchestratorLeavesTheOtherRecordedAndRestored(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")

	if err := app.StopOrchestrator(second); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != first {
		t.Fatalf("expected %q to still be recorded and reopened, got %q", first, target.OrchestratorID)
	}
	if len(target.AlsoReopen) != 0 {
		t.Fatalf("expected nothing else to reopen, got %v", target.AlsoReopen)
	}
}

func TestLegacySingleIDFileIsHonoured(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	if err := os.WriteFile(openPath, []byte(`{"orchestratorId":"`+id+`"}`), 0o644); err != nil {
		t.Fatalf("write legacy open file: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected the legacy scalar id to be reopened, got %q", target.OrchestratorID)
	}
	if len(target.AlsoReopen) != 0 {
		t.Fatalf("expected no others alongside a single legacy id, got %v", target.AlsoReopen)
	}
}

func TestRestartHandOffOverASetOfOpenOrchestratorsReopensTheRest(t *testing.T) {
	app, openPath, restoreDir := openStateTestApp(t)
	defer app.shutdown(context.Background())

	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")
	if err := writeOrchestratorRestoreTarget(restoreDir, orchestratorRestoreState{OrchestratorID: first}, time.Now()); err != nil {
		t.Fatalf("write restart hand-off: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != first {
		t.Fatalf("expected %q to own the pane, got %+v", first, target)
	}
	if len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != second {
		t.Fatalf("expected %q to reopen alongside it, got %v", second, target.AlsoReopen)
	}
	if got := orchestratorsvc.ReadOpenOrchestrators(openPath); len(got) != 2 {
		t.Fatalf("expected both still recorded open, got %v", got)
	}
}

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

// A restart re-records what it respawned.
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

func TestRestartHandOffStaysOneShotAndAgeBoundedOverTheDurableRecord(t *testing.T) {
	app, _, restoreDir := openStateTestApp(t)
	defer app.shutdown(context.Background())

	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")
	handOff := orchestratorRestoreState{OrchestratorID: first}
	if err := writeOrchestratorRestoreTarget(restoreDir, handOff, time.Now()); err != nil {
		t.Fatalf("write restart hand-off: %v", err)
	}

	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != first {
		t.Fatalf("expected the restart hand-off to win, got %+v", target)
	}
	// One-shot: the next launch falls back to the durable record.
	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != second {
		t.Fatalf("expected the hand-off to fire once and the durable record to answer, got %+v", target)
	}

	// Age-bounded.
	if err := writeOrchestratorRestoreTarget(restoreDir, handOff, time.Now().Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restart hand-off: %v", err)
	}
	if target := app.ResolveOrchestratorToReopen(); target.OrchestratorID != second {
		t.Fatalf("expected a stale hand-off to be dropped and the durable record to answer, got %+v", target)
	}
}
