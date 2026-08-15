package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// restartTestApp is an app whose restart records live in a temp dir and whose
// relaunch/quit are inert, so a test can drive the whole hand-off without
// spawning a second desktop.
func restartTestApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	restorePath := filepath.Join(home, orchestratorRestoreFileName)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		orchestratorRestorePath: restorePath,
		orchestratorOpenPath:    filepath.Join(home, orchestratorOpenFileName),
		relaunchApp:             func() error { return nil },
		quitApp:                 func() {},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, restorePath
}

// stageOrchestratorConversation writes the transcript the AI harness leaves for a
// conversation, which is how the resume path tells a conversation it can still
// continue from one that is gone.
func stageOrchestratorConversation(t *testing.T, conversationID string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	dir := filepath.Join(home, ".claude", "projects", "-orchestrators")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, conversationID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func readRestoreState(t *testing.T, path string) orchestratorRestoreState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restore file: %v", err)
	}
	var state orchestratorRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode restore file: %v", err)
	}
	return state
}

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

// Nothing was running for that id, so there is no conversation a resume could
// name — the restart reopens the orchestrator and hands it no task rather than
// telling whichever conversation the id resolves to to carry on.
func TestRestartAppWithNoLiveSessionRecordsNoTask(t *testing.T) {
	app, restorePath := restartTestApp(t)

	if err := app.RestartApp("agent-1"); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restorePath)
	if state.OrchestratorID != "agent-1" {
		t.Fatalf("expected the orchestrator to be recorded, got %+v", state)
	}
	if state.ConversationID != "" || state.ResumePrompt != "" {
		t.Fatalf("expected no conversation and no task without a live session, got %+v", state)
	}
}

// The restart records the conversation that asked for it and the scope that
// conversation is wired to, and resume delivers the task to that exact
// conversation.
func TestRestartAppRecordsTheLiveConversationAndResumesIt(t *testing.T) {
	app, restorePath := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restorePath)
	if state.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the live conversation to be recorded, got %+v", state)
	}
	if len(state.Environments) != 1 || state.Environments[0] != "frs/dev" {
		t.Fatalf("expected the live scope to be recorded, got %+v", state.Environments)
	}
	if state.ResumePrompt != orchestratorRestartResumePrompt {
		t.Fatalf("expected the restart to carry a task, got %q", state.ResumePrompt)
	}

	stageOrchestratorConversation(t, state.ConversationID)
	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id || target.ConversationID != state.ConversationID {
		t.Fatalf("expected the recorded conversation to be resumed, got %+v", target)
	}
	if target.ResumePrompt != orchestratorRestartResumePrompt || target.Notice != "" {
		t.Fatalf("expected the task to be delivered with no notice, got %+v", target)
	}
}

// The bug: an orchestrator id is mutable and reusable, so a hand-off recorded
// under one scope must not wake a conversation into another. The refusal is
// visible — the orchestrator still reopens, idle, carrying the reason.
func TestResumeIsRefusedWhenTheScopeChanged(t *testing.T) {
	app, restorePath := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	stageOrchestratorConversation(t, readRestoreState(t, restorePath).ConversationID)
	if _, err := app.UpdateOrchestrator(id, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected the orchestrator to still be reopened, got %+v", target)
	}
	if target.ResumePrompt != "" || target.ConversationID != "" {
		t.Fatalf("expected no task to be delivered into a changed scope, got %+v", target)
	}
	if !strings.Contains(target.Notice, "frs/dev") || !strings.Contains(target.Notice, "frs/laptop") {
		t.Fatalf("expected the notice to name both scopes, got %q", target.Notice)
	}
}

// A conversation that is no longer on disk cannot be the one that asked, so the
// task is withheld rather than delivered to whatever else the id would resume.
func TestResumeIsRefusedWhenTheConversationIsGone(t *testing.T) {
	app, _ := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected the orchestrator to still be reopened, got %+v", target)
	}
	if target.ResumePrompt != "" || target.Notice == "" {
		t.Fatalf("expected the task withheld with a visible reason, got %+v", target)
	}
}

func TestConsumeOrchestratorRestoreTargetIgnoresStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator-restore.json")
	now := time.Unix(1_700_000_000, 0)

	stale := orchestratorRestoreState{OrchestratorID: "agent-x"}
	if err := writeOrchestratorRestoreTarget(path, stale, now.Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restore target: %v", err)
	}
	if _, ok := consumeOrchestratorRestoreTarget(path, now); ok {
		t.Fatal("expected a stale target to be ignored")
	}

	fresh := orchestratorRestoreState{OrchestratorID: "agent-y"}
	if err := writeOrchestratorRestoreTarget(path, fresh, now); err != nil {
		t.Fatalf("write fresh restore target: %v", err)
	}
	got, ok := consumeOrchestratorRestoreTarget(path, now)
	if !ok || got.OrchestratorID != "agent-y" {
		t.Fatalf("expected fresh target agent-y, got %+v", got)
	}
}

// The hand-off round-trips everything resume needs to decide: which conversation
// asked, the scope it knew, and the task it staged.
func TestConsumeOrchestratorRestoreTargetCarriesTheHandOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator-restore.json")
	now := time.Unix(1_700_000_000, 0)
	written := orchestratorRestoreState{
		OrchestratorID: "va1",
		ConversationID: "conv-1",
		Environments:   []string{"erun/local", "petios/local"},
		ResumePrompt:   "verify the rebuild is live, then finish the task",
	}

	if err := writeOrchestratorRestoreTarget(path, written, now); err != nil {
		t.Fatalf("write restore target: %v", err)
	}
	got, ok := consumeOrchestratorRestoreTarget(path, now)
	if !ok {
		t.Fatal("expected the hand-off to be readable")
	}
	if got.OrchestratorID != written.OrchestratorID || got.ConversationID != written.ConversationID {
		t.Fatalf("expected the orchestrator and conversation to round-trip, got %+v", got)
	}
	if got.ResumePrompt != written.ResumePrompt {
		t.Fatalf("expected the resume prompt to round-trip, got %q", got.ResumePrompt)
	}
	if !equalOrchestratorScope(got.Environments, written.Environments) {
		t.Fatalf("expected the scope to round-trip, got %+v", got.Environments)
	}
}
