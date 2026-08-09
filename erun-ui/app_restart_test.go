package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartAppPersistsTargetRelaunchesAndQuits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	restorePath := filepath.Join(home, "orchestrator-restore.json")

	relaunched, quit := false, false
	app := NewApp(erunUIDeps{
		store:                   newOrchestratorStubStore(t.TempDir()),
		orchestratorRestorePath: restorePath,
		orchestratorOpenPath:    filepath.Join(home, "orchestrator-open.json"),
		relaunchApp:             func() error { relaunched = true; return nil },
		quitApp:                 func() { quit = true },
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

func TestConsumeOrchestratorRestoreTargetIgnoresStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator-restore.json")
	now := time.Unix(1_700_000_000, 0)

	if err := writeOrchestratorRestoreTarget(path, "agent-x", "", now.Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restore target: %v", err)
	}
	if got := consumeOrchestratorRestoreTarget(path, now); got.OrchestratorID != "" {
		t.Fatalf("expected a stale target to be ignored, got %q", got.OrchestratorID)
	}

	if err := writeOrchestratorRestoreTarget(path, "agent-y", "", now); err != nil {
		t.Fatalf("write fresh restore target: %v", err)
	}
	if got := consumeOrchestratorRestoreTarget(path, now); got.OrchestratorID != "agent-y" {
		t.Fatalf("expected fresh target agent-y, got %q", got.OrchestratorID)
	}
}

// A resume prompt round-trips through the restore file so the boot restore path
// can auto-continue the orchestrator's task after a rebuild+restart.
func TestConsumeOrchestratorRestoreTargetCarriesResumePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator-restore.json")
	now := time.Unix(1_700_000_000, 0)
	const prompt = "verify the rebuild is live, then finish the task"

	if err := writeOrchestratorRestoreTarget(path, "va1", prompt, now); err != nil {
		t.Fatalf("write restore target: %v", err)
	}
	got := consumeOrchestratorRestoreTarget(path, now)
	if got.OrchestratorID != "va1" {
		t.Fatalf("expected orchestrator va1, got %q", got.OrchestratorID)
	}
	if got.ResumePrompt != prompt {
		t.Fatalf("expected the resume prompt to round-trip, got %q", got.ResumePrompt)
	}
}
