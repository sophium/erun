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

// The measured failure. An orchestrator was spawned to resume its derived
// conversation; the harness did not adopt that id and kept writing to one of its
// own. Ten hours later the derived transcript was untouched and the other had
// been written seconds ago — and a restart resumed the dead one, coming back
// apparently healthy with none of the work in it.
func TestRestoreResumesTheConversationTheSessionIsLiveOnNotTheStaleDerivedOne(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	derived := orchestratorSessionID(id)
	// A v4 id, as the harness mints: the derivation could never produce it, which
	// is exactly why the derivation cannot answer what is live.
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	now := time.Now()
	stageOrchestratorConversationAt(t, derived, now.Add(-10*time.Hour))
	stageOrchestratorConversationAt(t, live, now.Add(-14*time.Second))
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: live,
		LaunchID:       recordedLaunchID(t, openPath, id),
		AtUnix:         now.Unix(),
	})

	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != id {
		t.Fatalf("expected %q reopened, got %q", id, target.OrchestratorID)
	}
	if target.ConversationID == derived {
		t.Fatalf("resumed the stale derived conversation %q while %q held the work", derived, live)
	}
	if target.ConversationID != live {
		t.Fatalf("expected the live conversation %q resumed, got %q", live, target.ConversationID)
	}
	// The two ids disagreed, and only the operator can tell whether the one that
	// won is the work they expect: coming back on a different conversation with
	// nothing said is the same defect wearing the opposite sign.
	for _, want := range []string{id, live, derived} {
		if !strings.Contains(target.Notice, want) {
			t.Fatalf("expected the notice to name %q, got %q", want, target.Notice)
		}
	}
}

// The ordinary click has to land where a restart lands. It resolved the derived
// id straight from the orchestrator id, so opening an orchestrator by hand
// stranded the very conversation a restart would have recovered.
func TestStartingAnOrchestratorResumesTheConversationItIsLiveOn(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	var launched []string
	app.deps.resolveOrchestratorLaunch = func(conversationID, _, _, _ string) (string, []string, error) {
		launched = append(launched, conversationID)
		return "claude-stub", nil, nil
	}

	id := createAndStartOrchestrator(t, app)
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, live)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: live,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	if err := app.StopOrchestrator(id); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}
	// Stopping forgets the entry, so the attach that follows is the operator
	// starting this orchestrator again from the sidebar.
	writeLiveConversationRecordEntry(t, app, id, live)
	if _, err := app.StartOrchestrator(id, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if got := launched[len(launched)-1]; got != live {
		t.Fatalf("expected the ordinary start to resume the live conversation %q, got %q", live, got)
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

// A conversation belongs to one orchestrator. A record that names another
// orchestrator's conversation is the failure that made the previous recorder
// worse than having none: it handed over somebody else's history and then
// confirmed the mistake on every later launch.
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
	if !strings.Contains(target.Notice, other.ID) {
		t.Fatalf("expected the notice to name the orchestrator that owns it, got %q", target.Notice)
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
	if target.Notice != "" {
		t.Fatalf("expected a first launch to say nothing, got %q", target.Notice)
	}
}

// A record from a launch that has been replaced — or from a writer that no
// longer exists at all, which is how a deleted recorder's files went on deciding
// resumes for days — is not this orchestrator's live conversation. It falls back
// to the anchor and NAMES the unconfirmed id, because that id is the operator's
// way back to the work.
func TestATrackedConversationFromAnotherLaunchIsNotSilentlyAuthoritative(t *testing.T) {
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

	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation, got %q", target.ConversationID)
	}
	if !strings.Contains(target.Notice, stranded) {
		t.Fatalf("expected the notice to name the unconfirmed conversation, got %q", target.Notice)
	}
}

// The habituation failure this whole notice family exists to avoid: a launch
// that resumes the SAME tracked conversation it already told the operator about
// has nothing new to say, and must say nothing. A notice that fires on every
// launch forever trains the operator to stop reading it -- including the three
// warning notices that share its surface and still need to be believed every
// time they fire.
func TestRestoreReportsARepeatedTrackedConversationOnlyOnce(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, live)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: live,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	first := app.ResolveOrchestratorToReopen()
	if first.ConversationID != live {
		t.Fatalf("expected the first launch to resume the live conversation %q, got %q", live, first.ConversationID)
	}
	if first.Notice == "" {
		t.Fatal("expected the first divergence between tracked and derived to be reported")
	}

	second := app.ResolveOrchestratorToReopen()
	if second.ConversationID != live {
		t.Fatalf("expected the second launch to resume the same live conversation %q, got %q", live, second.ConversationID)
	}
	if second.Notice != "" {
		t.Fatalf("expected a launch that resumes the conversation already reported to say nothing, got %q", second.Notice)
	}

	// The record still resolves the same tracked conversation; only the notice
	// about it is quiet the second time.
	third := app.ResolveOrchestratorToReopen()
	if third.Notice != "" {
		t.Fatalf("expected a third repeat launch to stay silent too, got %q", third.Notice)
	}
}

// A tracked conversation that changes AFTER already being reported is new
// information again and must be reported, even though the orchestrator has
// already been told about a previous divergence.
func TestRestoreReportsAChangeToADifferentTrackedConversation(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const firstLive = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	const secondLive = "22222222-3333-4444-8555-666666666666"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, firstLive)
	stageOrchestratorConversation(t, secondLive)
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: firstLive,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	if target := app.ResolveOrchestratorToReopen(); target.Notice == "" || target.ConversationID != firstLive {
		t.Fatalf("expected the first divergence reported and resumed, got conversation %q notice %q", target.ConversationID, target.Notice)
	}
	if target := app.ResolveOrchestratorToReopen(); target.Notice != "" {
		t.Fatalf("expected the repeat of the same tracked conversation to say nothing, got %q", target.Notice)
	}

	// A new launch under the SAME entry's launch id, whose session now reports a
	// different conversation than the one already reported.
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: secondLive,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})
	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID != secondLive {
		t.Fatalf("expected the changed tracked conversation %q resumed, got %q", secondLive, target.ConversationID)
	}
	if target.Notice == "" {
		t.Fatal("expected a change to a different tracked conversation to be reported")
	}
	if target := app.ResolveOrchestratorToReopen(); target.Notice != "" {
		t.Fatalf("expected the repeat of the newly reported conversation to say nothing, got %q", target.Notice)
	}
}

// A warning resolution is not a steady state to acclimatise to; it is
// something that just went wrong, and it must say so on every occurrence, not
// only the first. A record from an earlier, replaced launch stays wrong on
// every later launch too.
func TestAnUnconfirmedTrackedConversationIsReportedOnEveryLaunch(t *testing.T) {
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
		if !strings.Contains(target.Notice, stranded) {
			t.Fatalf("expected every launch to report the unconfirmed conversation, got %q", target.Notice)
		}
	}
}

// The same refusal for a record whose conversation is gone: resuming nothing is
// safer than resuming the wrong thing, and either way the operator hears it.
func TestATrackedConversationWithNoTranscriptFallsBackAndSaysSo(t *testing.T) {
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
	if !strings.Contains(target.Notice, "no longer on disk") {
		t.Fatalf("expected the notice to say the transcript is gone, got %q", target.Notice)
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
// RUN, and what the resolver then resolves is what that run left behind. A
// reader whose writer disappears fails here rather than in a month of quietly
// resuming the wrong conversation.
func TestTheLiveConversationRecorderWritesWhatTheResolverReads(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, live)

	// Exactly what a hook invocation carries on stdin, and exactly the two
	// environment values the launch hands the session.
	payload, err := json.Marshal(map[string]any{
		"session_id":      live,
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

	if target := app.ResolveOrchestratorToReopen(); target.ConversationID != live {
		t.Fatalf("the recorder wrote %q but the resolver resumed %q", live, target.ConversationID)
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
