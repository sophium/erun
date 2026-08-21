package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if got := readOpenOrchestrators(openPath); len(got) != 1 || got[0].OrchestratorID != id {
		t.Fatalf("expected [%q] on disk, got %v", id, got)
	}
}

// createAndStartNamedOrchestrator is createAndStartOrchestrator for a second
// orchestrator definition, so multi-orchestrator tests can start two distinct
// ones without colliding ids.
func createAndStartNamedOrchestrator(t *testing.T, app *App, name, environment string) string {
	t.Helper()
	created, err := app.CreateOrchestrator(name, []orchestratorEnvInput{{Tenant: "frs", Environment: environment}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	return created.ID
}

// The defect this issue fixes: the durable record was a scalar, so starting a
// second orchestrator discarded the record that the first was open, and a
// launch restored exactly one. Both must come back — one owning the pane, the
// other reopened alongside it — and the tab strip and the live sessions must
// agree about it.
func TestTwoOpenOrchestratorsAreBothRestoredOnPlainLaunch(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")

	target := app.ResolveOrchestratorToReopen()
	// The most recently started owns the pane; see app_restart.go for why.
	if target.OrchestratorID != second {
		t.Fatalf("expected %q (started last) to own the pane, got %q", second, target.OrchestratorID)
	}
	if len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != first {
		t.Fatalf("expected %q to also be reopened, got %v", first, target.AlsoReopen)
	}
	if got := readOpenOrchestrators(openPath); len(got) != 2 || got[0].OrchestratorID != first || got[1].OrchestratorID != second {
		t.Fatalf("expected both ids recorded oldest-first, got %v", got)
	}
}

// Stopping one orchestrator must not forget that the other is still open —
// the second, quieter bug the scalar shape caused: stopping the one currently
// recorded emptied the file entirely.
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

// An operator upgrading from a release that only ever wrote the single-id shape
// must not lose the one orchestrator they had open: the legacy scalar field is
// still understood, not discarded for being the wrong shape.
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

// The restart hand-off still names exactly one orchestrator to hand a prompt
// to, even when several were open: the rest of the set comes back too, but idle.
func TestRestartHandOffOverASetOfOpenOrchestratorsLeavesTheRestIdle(t *testing.T) {
	app, openPath, restoreDir := openStateTestApp(t)
	defer app.shutdown(context.Background())

	const prompt = "verify the rebuild is live, then finish the task"
	first := createAndStartOrchestrator(t, app)
	second := createAndStartNamedOrchestrator(t, app, "other", "laptop")
	conversationID := orchestratorSessionID(second)
	stageOrchestratorConversation(t, conversationID)
	handOff := orchestratorRestoreState{
		OrchestratorID: second,
		ConversationID: conversationID,
		Environments:   []string{"frs/laptop"},
		ResumePrompt:   prompt,
	}
	if err := writeOrchestratorRestoreTarget(restoreDir, handOff, time.Now()); err != nil {
		t.Fatalf("write restart hand-off: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != second || target.ResumePrompt != prompt {
		t.Fatalf("expected %q to own the pane with its prompt, got %+v", second, target)
	}
	if len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != first {
		t.Fatalf("expected %q to reopen idle alongside it, got %v", first, target.AlsoReopen)
	}
	if got := readOpenOrchestrators(openPath); len(got) != 2 {
		t.Fatalf("expected both still recorded open, got %v", got)
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
	if err := recordOpenOrchestrator(openPath, id, conversationID, []string{"frs/dev"}); err != nil {
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

// The bug: a plain launch used to re-derive a session id from the
// orchestrator id (uuid5(namespace, id)) and resume THAT, rather than the
// conversation actually recorded as running. A stale transcript that happens
// to sit at the derived id — left over from before the live session diverged
// from it, e.g. a resume that fell through to an unpinned launch — was
// silently resumed while the real, divergent conversation was left behind.
func TestPlainLaunchResumesTheRecordedConversationNotADerivedOne(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	staleDerived := orchestratorSessionID(id)
	stageOrchestratorConversation(t, staleDerived)
	liveConversation := "diverged-session-actually-running"
	stageOrchestratorConversation(t, liveConversation)
	if err := recordOpenOrchestrator(openPath, id, liveConversation, []string{"frs/dev"}); err != nil {
		t.Fatalf("record open orchestrator: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q to be reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID != liveConversation {
		t.Fatalf("expected the recorded live conversation %q to be resumed, not the derived id %q, got %q",
			liveConversation, staleDerived, target.ConversationID)
	}
}

// An orchestrator whose recorded conversation no longer exists on disk (pruned,
// deleted, never actually staged) starts fresh instead of falling back to
// whatever the derived id happens to already name — resuming nothing is safer
// than resuming a stale conversation.
func TestPlainLaunchStartsFreshWhenTheRecordedConversationIsGone(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	staleDerived := orchestratorSessionID(id)
	stageOrchestratorConversation(t, staleDerived)
	if err := recordOpenOrchestrator(openPath, id, "vanished-session", []string{"frs/dev"}); err != nil {
		t.Fatalf("record open orchestrator: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q to be reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID == "" || target.ConversationID == staleDerived || target.ConversationID == "vanished-session" {
		t.Fatalf("expected a fresh conversation, neither the stale derived id nor the vanished one, got %q",
			target.ConversationID)
	}
}

// An operator upgrading from a release that recorded only the orchestrator id
// (an earlier shape, or the scalar one before it) must not have that silence
// read as permission to resume whatever the derived id names. The very first
// restore under the new record starts every such orchestrator fresh.
func TestUpgradingFromARecordWithNoSessionIDStartsFresh(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	staleDerived := orchestratorSessionID(id)
	stageOrchestratorConversation(t, staleDerived)
	if err := os.WriteFile(openPath, []byte(`{"orchestratorIds":["`+id+`"]}`), 0o644); err != nil {
		t.Fatalf("write legacy open file: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q to be reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID == "" || target.ConversationID == staleDerived {
		t.Fatalf("expected a fresh conversation rather than the stale derived id, got %q", target.ConversationID)
	}
}

// The defect as actually reported: a plain restore (not a restart hand-off)
// reopened a re-scoped orchestrator's stale conversation with no note and no
// task — nothing at all said its environments had moved out from under it. A
// plain reopen still resumes that conversation idle (the same
// conservative-but-not-destructive choice a refused hand-off makes), but the
// scope change must be visible in the notice.
func TestPlainReopenSurfacesANoticeWhenScopeChanged(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	liveConversation := orchestratorSessionID(id)
	stageOrchestratorConversation(t, liveConversation)
	if _, err := app.UpdateOrchestrator(id, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q to be reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID != liveConversation {
		t.Fatalf("expected the orchestrator's own conversation to still be resumed idle, got %+v", target)
	}
	if target.ResumePrompt != "" {
		t.Fatalf("expected no task delivered on a plain reopen, got %+v", target)
	}
	if !strings.Contains(target.Notice, "frs/dev") || !strings.Contains(target.Notice, "frs/laptop") {
		t.Fatalf("expected the notice to name both scopes, got %q", target.Notice)
	}
}

// The AlsoReopen case: restoring every orchestrator that was open means an
// entry there is exactly the no-note, no-task case a re-scoped id can
// silently resume into — the tabs that were not the reason for the restart
// are the ones a resume prompt never reaches, so the scope check on them
// cannot piggyback on a hand-off refusal and has to run on its own.
func TestAlsoReopenSurfacesANoticeWhenScopeChanged(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	stale := createAndStartOrchestrator(t, app)
	staleConversation := orchestratorSessionID(stale)
	stageOrchestratorConversation(t, staleConversation)
	current := createAndStartNamedOrchestrator(t, app, "other", "laptop")
	if _, err := app.UpdateOrchestrator(stale, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	// current owns the pane (started last); stale comes back via AlsoReopen.
	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != current {
		t.Fatalf("expected %q (started last) to own the pane, got %q", current, target.OrchestratorID)
	}
	if len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != stale {
		t.Fatalf("expected %q to also be reopened, got %v", stale, target.AlsoReopen)
	}
	if target.AlsoReopen[0].ConversationID != staleConversation {
		t.Fatalf("expected %q to still resume its own conversation idle, got %q", stale, target.AlsoReopen[0].ConversationID)
	}
	if !strings.Contains(target.Notice, stale) || !strings.Contains(target.Notice, "frs/dev") || !strings.Contains(target.Notice, "frs/laptop") {
		t.Fatalf("expected the notice to name %q and both scopes, got %q", stale, target.Notice)
	}
}
