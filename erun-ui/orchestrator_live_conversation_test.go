package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stageOrchestratorConversationAt stages a transcript and backdates it, so a
// fixture can reproduce the shape that made this a bug: one conversation that
// stopped growing hours ago beside one that was written seconds ago.
func stageOrchestratorConversationAt(t *testing.T, conversationID string, written time.Time) {
	t.Helper()
	stageOrchestratorConversation(t, conversationID)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	path := filepath.Join(home, ".claude", "projects", "-orchestrators", conversationID+".jsonl")
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatalf("backdate transcript: %v", err)
	}
}

// recordedLaunchID reads the nonce the desktop itself minted for an
// orchestrator's last launch. A fixture that invented one would prove nothing:
// the whole mechanism is that the two halves of the record name the SAME launch,
// and only the launch can say what that is.
func recordedLaunchID(t *testing.T, openPath, orchestratorID string) string {
	t.Helper()
	launch := strings.TrimSpace(orchestratorEntryOrEmpty(readOpenOrchestrators(openPath), orchestratorID).LaunchID)
	if launch == "" {
		t.Fatalf("the launch of %s recorded no launch id, so nothing could ever confirm what its session reports", orchestratorID)
	}
	return launch
}

// writeLiveConversationRecord stages what an orchestrator's own session reports
// through its hooks.
func writeLiveConversationRecord(t *testing.T, orchestratorID string, record orchestratorLiveConversation) {
	t.Helper()
	path := orchestratorLiveConversationPath(orchestratorID)
	if path == "" {
		t.Fatalf("no live-conversation path for %q", orchestratorID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create live-conversation dir: %v", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode live-conversation record: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write live-conversation record: %v", err)
	}
}

// The measured trade-off (erun#1696): an orchestrator's session diverged onto a
// conversation of its own -- the derived transcript stopped growing ten hours
// ago and the other has been written seconds ago -- and a launch resumes the
// derived anchor anyway, every time, with nothing said about it. The tracked
// conversation is not lost: it stays recorded and is offered in the Manage
// dialog (erun-ui/orchestrator_conversations_test.go) for the operator to
// attach deliberately. What it no longer does is override the anchor on its
// own say-so, because nothing then moves a drifted record back and a launch
// used to keep resuming it forever.
func TestRestoreAlwaysResumesTheDerivedAnchorEvenWhenATrackedConversationDiverged(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	derived := orchestratorSessionID(id)
	// A v4 id, as the harness mints: the derivation could never produce it, which
	// is exactly why a session can drift onto one.
	const diverged = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	now := time.Now()
	stageOrchestratorConversationAt(t, derived, now.Add(-10*time.Hour))
	stageOrchestratorConversationAt(t, diverged, now.Add(-14*time.Second))
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: diverged,
		LaunchID:       recordedLaunchID(t, openPath, id),
		AtUnix:         now.Unix(),
	})

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID != derived {
		t.Fatalf("expected the derived anchor %q resumed regardless of the divergence, got %q", derived, target.ConversationID)
	}
	if noticeText(target.Notices) != "" {
		t.Fatalf("expected an ordinary launch to say nothing about a tracked conversation it never adopts, got %q", noticeText(target.Notices))
	}
}

// The ordinary click has to land where a restart lands. Both go through
// resolveOrchestratorConversation, so both now resume the derived anchor
// regardless of what the orchestrator's session last reported.
func TestStartingAnOrchestratorResumesTheDerivedAnchorNotADivergedConversation(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	var launched []string
	app.deps.resolveOrchestratorLaunch = func(conversationID, _, _, _ string) (string, []string, error) {
		launched = append(launched, conversationID)
		return "claude-stub", nil, nil
	}

	id := createAndStartOrchestrator(t, app)
	derived := orchestratorSessionID(id)
	const diverged = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, derived)
	stageOrchestratorConversation(t, diverged)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: diverged,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	if err := app.StopOrchestrator(id); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}
	// Stopping forgets the entry, so the start that follows is the operator
	// starting this orchestrator again from the sidebar.
	writeLiveConversationRecordEntry(t, app, id, diverged)
	if _, err := app.StartOrchestrator(id, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if got := launched[len(launched)-1]; got != derived {
		t.Fatalf("expected the ordinary start to resume the derived anchor %q, got %q", derived, got)
	}
}

// writeLiveConversationRecordEntry re-records an orchestrator as open with a
// launch of its own and points its live record at conversationID, which is what
// a session under that launch would have reported.
func writeLiveConversationRecordEntry(t *testing.T, app *App, orchestratorID, conversationID string) {
	t.Helper()
	const launch = "launch-under-test"
	if err := recordOpenOrchestrator(app.deps.orchestratorOpenPath, orchestratorID, launch, []string{"frs/dev"}); err != nil {
		t.Fatalf("record open orchestrator: %v", err)
	}
	writeLiveConversationRecord(t, orchestratorID, orchestratorLiveConversation{
		ConversationID: conversationID,
		LaunchID:       launch,
	})
}

// A conversation belongs to one orchestrator. A tracked record that names
// another orchestrator's conversation is never even consulted by an ordinary
// resolve any more, so it cannot hand it over -- the ownership check still
// matters for the Manage dialog's listing and for an explicit attach
// (erun-ui/orchestrator_conversations_test.go), never for automatic
// resolution.
func TestRestoreNeverResumesAnotherOrchestratorsConversation(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	other, err := app.CreateOrchestrator("stranger", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	theirs := orchestratorSessionID(other.ID)
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, theirs)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: theirs,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID == theirs {
		t.Fatal("resumed another orchestrator's conversation")
	}
	if target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected its own derived conversation, got %q", target.ConversationID)
	}
	if noticeText(target.Notices) != "" {
		t.Fatalf("expected an ordinary launch to say nothing, got %q", noticeText(target.Notices))
	}
}

// The anchor still anchors. A first launch has nothing recorded, and resolving to
// the derived conversation is the whole point of deriving one — so it happens
// with nothing said, because nothing surprising happened.
func TestAFirstLaunchWithNothingTrackedResumesTheDerivedConversation(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)

	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation %q, got %q", orchestratorSessionID(id), target.ConversationID)
	}
	if noticeText(target.Notices) != "" {
		t.Fatalf("expected a first launch to say nothing, got %q", noticeText(target.Notices))
	}
}

// A record from a launch that has been replaced — or from a writer that no
// longer exists at all, which is how a deleted recorder's files went on
// deciding resumes for days — is just as silently ignored by an ordinary
// resolve as a confirmed one: neither is consulted for automatic resumption
// any more (erun#1696). Its confirmation status still matters to the Manage
// dialog's listing (erun-ui/orchestrator_conversations_test.go), which is
// where an unconfirmed record is distinguished from a confirmed one, but an
// ordinary launch resumes the anchor either way with nothing to say.
func TestAnUnconfirmedTrackedConversationIsNeverConsultedByAnOrdinaryResolve(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const stranded = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, stranded)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: stranded,
		LaunchID:       "a-launch-that-is-not-this-one",
	})

	first := app.ResolveOrchestratorToReopen()
	second := app.ResolveOrchestratorToReopen()
	for _, target := range []relaunchTarget{first, second} {
		if target.ConversationID != orchestratorSessionID(id) {
			t.Fatalf("expected the derived conversation, got %q", target.ConversationID)
		}
		if noticeText(target.Notices) != "" {
			t.Fatalf("expected every ordinary launch to say nothing, got %q", noticeText(target.Notices))
		}
	}
}

// The same silence for a tracked record whose conversation is gone: an
// ordinary resolve never looks at the tracked record at all, so it neither
// resumes it nor reports on its absence.
func TestATrackedConversationWithNoTranscriptIsAlsoIgnoredByAnOrdinaryResolve(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec",
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation, got %q", target.ConversationID)
	}
	if noticeText(target.Notices) != "" {
		t.Fatalf("expected an ordinary launch to say nothing, got %q", noticeText(target.Notices))
	}
}

// "Newest transcript in the directory" is not the answer and must never become
// it: the orchestrators project directory holds conversations belonging to other
// orchestrators and to nobody at all.
func TestANewerUnclaimedConversationIsNeverResumed(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	now := time.Now()
	stageOrchestratorConversationAt(t, orchestratorSessionID(id), now.Add(-10*time.Hour))
	stageOrchestratorConversationAt(t, "11111111-2222-4333-8444-555555555555", now)

	if target := app.ResolveOrchestratorToReopen(); target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation, got %q", target.ConversationID)
	}
}

// A restart takes a live session away and promises it back. What it records has
// to be the conversation that session is really on, not the id it was launched
// with — the hand-off is the one path that must reach the session that asked for
// it.
func TestRestartHandoffNamesTheConversationTheSessionIsLiveOn(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	id := createAndStartOrchestrator(t, app)
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, live)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: live,
		LaunchID:       recordedLaunchID(t, app.deps.orchestratorOpenPath, id),
	})

	if err := app.RestartApp(id); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}

	if state := readRestoreState(t, restoreDir, id); state.ConversationID != live {
		t.Fatalf("expected the hand-off to name the live conversation %q, got %+v", live, state)
	}
}

// The structural gate, and the reason this record is not the deleted one coming
// back: the writer is exercised, not asserted about. The hook erun installs is
// RUN, and what the Manage dialog's listing then shows confirmed is what that
// run left behind. A reader whose writer disappears fails here rather than in
// a month of a stranded conversation nobody could find. Reading through
// ListOrchestratorConversations rather than ResolveOrchestratorToReopen is
// itself the point of erun#1696: the tracked record is confirmable, but no
// longer resumed automatically.
func TestTheLiveConversationRecorderWritesWhatTheListingReadsConfirmed(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	derived := orchestratorSessionID(id)
	const diverged = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, derived)
	stageOrchestratorConversation(t, diverged)

	// Exactly what a hook invocation carries on stdin, and exactly the two
	// environment values the launch hands the session.
	payload, err := json.Marshal(map[string]any{
		"session_id":      diverged,
		"transcript_path": "/does/not/matter.jsonl",
		"hook_event_name": "SessionStart",
	})
	if err != nil {
		t.Fatalf("encode hook input: %v", err)
	}
	cmd := exec.Command(shell, "-c", orchestratorLiveConversationHookCommand())
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(os.Environ(),
		"ERUN_ORCHESTRATOR_ID="+id,
		orchestratorLaunchEnvVar+"="+recordedLaunchID(t, openPath, id))
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run recorder hook: %v\n%s", runErr, out)
	}

	listing, err := app.ListOrchestratorConversations(id)
	if err != nil {
		t.Fatalf("ListOrchestratorConversations failed: %v", err)
	}
	found := false
	for _, row := range listing.Conversations {
		if row.ConversationID != diverged {
			continue
		}
		found = true
		if row.Role != orchestratorConversationRoleLive {
			t.Fatalf("the recorder wrote %q under a confirmed launch, expected it listed as confirmed, got %+v", diverged, row)
		}
	}
	if !found {
		t.Fatalf("the recorder wrote %q but the listing never surfaced it: %+v", diverged, listing.Conversations)
	}

	// Confirmable, but still not what an ordinary launch resumes.
	if target := app.ResolveOrchestratorToReopen(); target.ConversationID != derived {
		t.Fatalf("expected the derived anchor resumed regardless of the recorded conversation, got %q", target.ConversationID)
	}
}

// A session that never saw this launch's nonce cannot claim the orchestrator's
// conversation, however it came by the orchestrator id. This is the ownership
// hole in the deleted recorder, closed at the writer.
func TestTheRecorderWritesNothingWithoutTheLaunchItBelongsTo(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	cmd := exec.Command(shell, "-c", orchestratorLiveConversationHookCommand())
	cmd.Stdin = strings.NewReader(`{"session_id":"0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"}`)
	cmd.Env = append(os.Environ(), "ERUN_ORCHESTRATOR_ID="+id, orchestratorLaunchEnvVar+"=")
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run recorder hook: %v\n%s", runErr, out)
	}

	if _, ok := readOrchestratorLiveConversation(id); ok {
		t.Fatal("a session with no launch of ours recorded itself as this orchestrator's live conversation")
	}
}

// The recorder is installed on every boundary where the answer can change: the
// session boundaries (start, resume, clear, compact) and the turn boundaries in
// between, because a session that moves to a new id mid-run is the case this
// exists for.
func TestOrchestratorLiveConversationRecorderIsInstalledWhereItIsRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := ensureOrchestratorSessionStartHook(dir); err != nil {
		t.Fatalf("ensure hooks: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"} {
		recorders := 0
		for _, block := range settings.Hooks[event] {
			if isOrchestratorLiveConversationHookBlock(block) {
				recorders++
			}
		}
		if recorders != 1 {
			t.Fatalf("%s carries %d live-conversation recorders, want exactly 1:\n%s", event, recorders, raw)
		}
	}
}
