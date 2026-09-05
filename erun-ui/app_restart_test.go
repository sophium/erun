package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
	restoreDir := filepath.Join(home, "state", orchestratorRestoreDirName)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		orchestratorRestoreDir: restoreDir,
		orchestratorOpenPath:   filepath.Join(home, orchestratorOpenFileName),
		relaunchApp:            func() error { return nil },
		quitApp:                func() {},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, restoreDir
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

// noticeText joins every notice's text with a space, so a test that only cares
// whether some notice named a given substring can keep asserting on one
// string rather than searching a slice by hand.
func noticeText(notices []orchestratorNotice) string {
	texts := make([]string, 0, len(notices))
	for _, notice := range notices {
		texts = append(texts, notice.Text)
	}
	return strings.Join(texts, " ")
}

// stagedRestoreIDs lists the orchestrators with a hand-off still on disk, so a
// test can assert that consuming one did not silently take another with it.
func stagedRestoreIDs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read restore dir: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(out)
	return out
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

// Nothing was running for that id, so there is no conversation a resume could
// name — the restart reopens the orchestrator and hands it no task rather than
// telling whichever conversation the id resolves to to carry on.
func TestRestartAppWithNoLiveSessionRecordsNoTask(t *testing.T) {
	app, restoreDir := restartTestApp(t)

	if err := app.RestartApp("agent-1"); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restoreDir, "agent-1")
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
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restoreDir, id)
	if state.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the live conversation to be recorded, got %+v", state)
	}
	if len(state.Environments) != 1 || state.Environments[0] != "frs/dev" {
		t.Fatalf("expected the live scope to be recorded, got %+v", state.Environments)
	}
	if state.ResumePrompt != orchestratorRestartResumePrompt(id) {
		t.Fatalf("expected the restart to carry a task, got %q", state.ResumePrompt)
	}

	stageOrchestratorConversation(t, state.ConversationID)
	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id || target.ConversationID != state.ConversationID {
		t.Fatalf("expected the recorded conversation to be resumed, got %+v", target)
	}
	if target.ResumePrompt != orchestratorRestartResumePrompt(id) || noticeText(target.Notices) != "" {
		t.Fatalf("expected the task to be delivered with no notice, got %+v", target)
	}
	// The resume prompt has to name the note it means: the working directory is
	// shared, so "the note you wrote here" is satisfied by anyone's.
	assertNamesOwnNote(t, "the resume prompt", id, target.ResumePrompt)
	assertNoHandOffsLeftStaged(t, restoreDir)
}

// A restart resumes the orchestrator's OWN conversation, derived from its id.
// This is the whole contract: the derivation is a pure function, so it is the
// same on every launch and there is nothing on disk that can point it
// elsewhere.
func TestRestartHandoffResumesTheOrchestratorsDerivedConversation(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restoreDir, id)
	if state.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation %q, got %+v", orchestratorSessionID(id), state)
	}
}

// The failure this replaced: a record could name ANOTHER orchestrator's
// conversation, and a restart adopted it -- handing this orchestrator somebody
// else's history, and then confirming the mistake on every later launch. A
// conversation derived for one orchestrator can never be resumed for another.
func TestRestartHandoffNeverResumesAnotherOrchestratorsConversation(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	stranger := orchestratorSessionID("some-other-orchestrator")
	stageOrchestratorConversation(t, stranger)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	state := readRestoreState(t, restoreDir, id)
	if state.ConversationID == stranger {
		t.Fatal("resumed another orchestrator's conversation")
	}
	if state.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected its own derived conversation, got %+v", state)
	}
}

// assertNamesOwnNote fails unless text points at the return note belonging to
// this orchestrator rather than at the shared directory holding everyone's.
func assertNamesOwnNote(t *testing.T, what, orchestratorID, text string) {
	t.Helper()
	note := orchestratorReturnNoteName(orchestratorID)
	if !strings.Contains(text, note) {
		t.Fatalf("expected %s to name %q, got %q", what, note, text)
	}
}

// assertNoHandOffsLeftStaged checks the launch cleared every hand-off it read. A
// hand-off left behind would fire at a later launch, against a world that has
// moved on.
func assertNoHandOffsLeftStaged(t *testing.T, restoreDir string) {
	t.Helper()
	if ids := stagedRestoreIDs(t, restoreDir); len(ids) != 0 {
		t.Fatalf("expected every hand-off to be consumed, got %v still staged", ids)
	}
}

// restartTwoOrchestrators starts two orchestrators and has each trigger a
// rebuild+restart, which under the shared slot meant the second wrote over the
// first. Returns their ids in creation order.
func restartTwoOrchestrators(t *testing.T) (*App, string, string, string) {
	t.Helper()
	app, restoreDir := restartTestApp(t)
	first := createAndStartOrchestrator(t, app)
	second, err := app.CreateOrchestrator("other", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(second.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	for _, id := range []string{first, second.ID} {
		if err := app.RestartApp(id); err != nil {
			t.Fatalf("RestartApp failed for %s: %v", id, err)
		}
	}
	return app, restoreDir, first, second.ID
}

// The bug: several orchestrators shared one restart slot, so the second restart
// replaced the first and that orchestrator was simply never resumed. Each now
// stages its own hand-off, naming its own conversation and its own note.
func TestConcurrentRestartsStageAHandOffEach(t *testing.T) {
	_, restoreDir, first, second := restartTwoOrchestrators(t)

	staged := stagedRestoreIDs(t, restoreDir)
	if len(staged) != 2 || staged[0] != first || staged[1] != second {
		t.Fatalf("expected both hand-offs staged, got %v", staged)
	}
	firstState := readRestoreState(t, restoreDir, first)
	secondState := readRestoreState(t, restoreDir, second)
	if firstState.ConversationID == secondState.ConversationID {
		t.Fatalf("expected each hand-off to name its own conversation, got %q twice", firstState.ConversationID)
	}
	assertNamesOwnNote(t, first+"'s prompt", first, firstState.ResumePrompt)
	assertNamesOwnNote(t, second+"'s prompt", second, secondState.ResumePrompt)
}

// A launch reopens one orchestrator, so one hand-off is delivered — whole, with
// its own conversation and its own note — and the other is named in the notice
// rather than disappearing with the session that staged it.
func TestTheHandOffThatIsNotReopenedIsReported(t *testing.T) {
	app, restoreDir, first, second := restartTwoOrchestrators(t)
	stageOrchestratorConversation(t, orchestratorSessionID(first))
	stageOrchestratorConversation(t, orchestratorSessionID(second))

	target := app.ResolveOrchestratorToReopen()
	delivered, notReopened := second, first
	if target.OrchestratorID == first {
		delivered, notReopened = first, second
	} else if target.OrchestratorID != second {
		t.Fatalf("expected one of the two restarts to be reopened, got %+v", target)
	}
	if target.ConversationID != orchestratorSessionID(delivered) {
		t.Fatalf("expected %s's own conversation to be resumed, got %+v", delivered, target)
	}
	assertNamesOwnNote(t, "the resume prompt", delivered, target.ResumePrompt)
	if !strings.Contains(noticeText(target.Notices), notReopened) {
		t.Fatalf("expected the notice to name %s, got %q", notReopened, noticeText(target.Notices))
	}
	assertNamesOwnNote(t, "the notice", notReopened, noticeText(target.Notices))
	assertNoHandOffsLeftStaged(t, restoreDir)
}

// Which of several pending hand-offs a launch acts on is the most recent one —
// the restart the operator triggered last.
func TestTheMostRecentHandOffIsTheOneDelivered(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	for _, staged := range []struct {
		id  string
		age time.Duration
	}{{"zeta", 3 * time.Minute}, {"alpha", time.Minute}, {"mid", 2 * time.Minute}} {
		state := orchestratorRestoreState{OrchestratorID: staged.id, ResumePrompt: "carry on"}
		if err := writeOrchestratorRestoreTarget(dir, state, now.Add(-staged.age)); err != nil {
			t.Fatalf("write hand-off for %s: %v", staged.id, err)
		}
	}

	delivered, notReopened := consumeOrchestratorRestoreTargets(dir, now)
	if delivered.OrchestratorID != "alpha" {
		t.Fatalf("expected the newest hand-off to be delivered, got %q", delivered.OrchestratorID)
	}
	if len(notReopened) != 2 || notReopened[0].OrchestratorID != "mid" || notReopened[1].OrchestratorID != "zeta" {
		t.Fatalf("expected the older hand-offs returned newest-first, got %+v", notReopened)
	}
}

// The upgrade that introduces the per-orchestrator slot arrives through a
// restart the previous release staged in the single shared one, so that slot is
// still read once — otherwise the fix loses exactly the hand-off delivering it.
func TestHandOffLeftInTheSharedSlotIsStillDeliveredOnce(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)
	conversationID := orchestratorSessionID(id)
	stageOrchestratorConversation(t, conversationID)

	legacy := legacyOrchestratorRestorePath(restoreDir)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	data, err := json.Marshal(orchestratorRestoreState{
		OrchestratorID: id,
		ConversationID: conversationID,
		Environments:   []string{"frs/dev"},
		ResumePrompt:   "finish the task",
		SavedAtUnix:    time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("encode legacy hand-off: %v", err)
	}
	if err := os.WriteFile(legacy, data, 0o644); err != nil {
		t.Fatalf("write legacy hand-off: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id || target.ResumePrompt != "finish the task" {
		t.Fatalf("expected the shared-slot hand-off to be delivered, got %+v", target)
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

// The bug: an orchestrator id is mutable and reusable, so a hand-off recorded
// under one scope must not wake a conversation into another. The refusal is
// visible — the orchestrator still reopens, idle, carrying the reason. It still
// resumes its own last-recorded conversation rather than nothing: the refusal
// is about not auto-running a task into the wrong scope, not about whether the
// conversation itself is safe to reopen idle.
func TestResumeIsRefusedWhenTheScopeChanged(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	liveConversation := readRestoreState(t, restoreDir, id).ConversationID
	stageOrchestratorConversation(t, liveConversation)
	if _, err := app.UpdateOrchestrator(id, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected the orchestrator to still be reopened, got %+v", target)
	}
	if target.ResumePrompt != "" {
		t.Fatalf("expected no task to be delivered into a changed scope, got %+v", target)
	}
	if target.ConversationID != liveConversation {
		t.Fatalf("expected the orchestrator's own conversation to still be resumed idle, got %+v", target)
	}
	if !strings.Contains(noticeText(target.Notices), "frs/dev") || !strings.Contains(noticeText(target.Notices), "frs/laptop") {
		t.Fatalf("expected the notice to name both scopes, got %q", noticeText(target.Notices))
	}
	// A refusal points at the note it declined to act on, by name — the operator
	// is reading it in a directory holding every orchestrator's.
	if !strings.Contains(noticeText(target.Notices), orchestratorReturnNoteName(id)) {
		t.Fatalf("expected the notice to name %q, got %q", orchestratorReturnNoteName(id), noticeText(target.Notices))
	}
	// A refusal is not a steady state; it must never read at the same severity
	// as a routine, successful resume.
	assertAllNoticesAreWarnings(t, target.Notices)
}

// assertAllNoticesAreWarnings fails unless every notice given is a warning —
// used where a refused hand-off leaves nothing routine to report.
func assertAllNoticesAreWarnings(t *testing.T, notices []orchestratorNotice) {
	t.Helper()
	for _, notice := range notices {
		if notice.Kind != orchestratorNoticeWarning {
			t.Fatalf("expected every notice to be a warning, got %+v", notice)
		}
	}
}

// The heart of the bug this fixes: several orchestrators resolving on the same
// restore must each carry their own notice, attributed to the right one rather
// than flattened into one string. Both causes here are warnings -- a launch
// resumes the derived anchor with nothing to report unless something the
// operator asked for could not be honoured (erun#1696), so a mixed restore's
// two notices are two warnings, not a routine one beside a genuine one.
func TestMixedRestoreAttributesEachWarningToItsOwnOrchestrator(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	stale := createAndStartOrchestrator(t, app)
	stageOrchestratorConversation(t, orchestratorSessionID(stale))
	if _, err := app.UpdateOrchestrator(stale, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "laptop"}}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	current := createAndStartNamedOrchestrator(t, app, "other", "laptop")
	const gone = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(current))
	if err := setAttachedOrchestratorConversation(openPath, current, gone); err != nil {
		t.Fatalf("attach: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != current {
		t.Fatalf("expected %q (started last) to own the pane, got %q", current, target.OrchestratorID)
	}
	assertTwoWarningsEachAttributed(t, target.Notices, current, stale)
}

// assertTwoWarningsEachAttributed fails unless notices carries exactly two
// warning notices, one attributed to each of the given orchestrators, kept
// distinct rather than merged into one.
func assertTwoWarningsEachAttributed(t *testing.T, notices []orchestratorNotice, first, second string) {
	t.Helper()
	seen := map[string]bool{}
	for _, notice := range notices {
		if notice.Kind != orchestratorNoticeWarning {
			t.Fatalf("expected every notice here to be a warning, got %+v", notice)
		}
		seen[notice.OrchestratorID] = true
	}
	if len(notices) != 2 || !seen[first] || !seen[second] {
		t.Fatalf("expected one warning for %q (unhonourable attachment) and one for %q (changed scope), got %+v",
			first, second, notices)
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
	if target.ResumePrompt != "" || noticeText(target.Notices) == "" {
		t.Fatalf("expected the task withheld with a visible reason, got %+v", target)
	}
}

func TestConsumeOrchestratorRestoreTargetIgnoresStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)

	stale := orchestratorRestoreState{OrchestratorID: "agent-x"}
	if err := writeOrchestratorRestoreTarget(dir, stale, now.Add(-2*orchestratorRestoreMaxAge)); err != nil {
		t.Fatalf("write stale restore target: %v", err)
	}
	if got, _ := consumeOrchestratorRestoreTargets(dir, now); got.OrchestratorID != "" {
		t.Fatalf("expected a stale target to be ignored, got %+v", got)
	}

	fresh := orchestratorRestoreState{OrchestratorID: "agent-y"}
	if err := writeOrchestratorRestoreTarget(dir, fresh, now); err != nil {
		t.Fatalf("write fresh restore target: %v", err)
	}
	got, _ := consumeOrchestratorRestoreTargets(dir, now)
	if got.OrchestratorID != "agent-y" {
		t.Fatalf("expected fresh target agent-y, got %+v", got)
	}
}

// The hand-off round-trips everything resume needs to decide: which conversation
// asked, the scope it knew, and the task it staged.
func TestConsumeOrchestratorRestoreTargetCarriesTheHandOff(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	written := orchestratorRestoreState{
		OrchestratorID: "va1",
		ConversationID: "conv-1",
		Environments:   []string{"erun/local", "petios/local"},
		ResumePrompt:   "verify the rebuild is live, then finish the task",
	}

	if err := writeOrchestratorRestoreTarget(dir, written, now); err != nil {
		t.Fatalf("write restore target: %v", err)
	}
	got, _ := consumeOrchestratorRestoreTargets(dir, now)
	if got.OrchestratorID == "" {
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
