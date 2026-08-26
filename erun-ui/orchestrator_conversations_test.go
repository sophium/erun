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

// stageTranscriptIn writes a transcript into a named project directory, with the
// records the harness actually writes: the working directory it was started in
// and the first thing the operator said.
func stageTranscriptIn(t *testing.T, projectDir, conversationID, cwd, firstPrompt string, written time.Time) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	dir := filepath.Join(home, ".claude", "projects", projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create transcript dir: %v", err)
	}
	records := []map[string]any{
		{"type": "queue-operation", "sessionId": conversationID, "content": firstPrompt},
		{"type": "user", "cwd": cwd, "sessionId": conversationID, "message": map[string]any{"content": firstPrompt}},
	}
	var encoded []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode transcript record: %v", err)
		}
		encoded = append(append(encoded, line...), '\n')
	}
	path := filepath.Join(dir, conversationID+".jsonl")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatalf("backdate transcript: %v", err)
	}
	return path
}

// conversationRow finds one row in a listing.
func conversationRow(t *testing.T, listing orchestratorConversations, conversationID string) orchestratorConversation {
	t.Helper()
	for _, row := range listing.Conversations {
		if row.ConversationID == conversationID {
			return row
		}
	}
	t.Fatalf("conversation %q is not in the listing: %+v", conversationID, listing.Conversations)
	return orchestratorConversation{}
}

// listedConversationIDs is the listing as a set, for absence assertions.
func listedConversationIDs(listing orchestratorConversations) map[string]struct{} {
	out := make(map[string]struct{}, len(listing.Conversations))
	for _, row := range listing.Conversations {
		out[row.ConversationID] = struct{}{}
	}
	return out
}

// The operator's question is "which of these is the work?", so a row has to
// carry what answers it: when it was last written, how big it is, where it was
// started, how it opens, and what relationship this orchestrator has to it.
func TestConversationListingSaysWhichIsLiveAndWhatEachOneIs(t *testing.T) {
	app, openPath, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	derived := orchestratorSessionID(id)
	const live = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	now := time.Now()
	stageTranscriptIn(t, "-orchestrators", derived, "/Users/me/orchestrators", "the stale one", now.Add(-10*time.Hour))
	stageTranscriptIn(t, "-orchestrators", live, "/Users/me/orchestrators", "carry the release through", now.Add(-14*time.Second))
	writeLiveConversationRecord(t, id, orchestratorLiveConversation{
		ConversationID: live,
		LaunchID:       recordedLaunchID(t, openPath, id),
	})

	listing, err := app.ListOrchestratorConversations(id)
	if err != nil {
		t.Fatalf("ListOrchestratorConversations failed: %v", err)
	}
	if listing.Resuming != live || listing.ResumingSource != string(orchestratorConversationTracked) {
		t.Fatalf("expected the listing to report the live conversation as what it resumes, got %+v", listing)
	}
	// Newest first, because the operator scans for the one that was written last.
	if listing.Conversations[0].ConversationID != live {
		t.Fatalf("expected the most recently written conversation first, got %+v", listing.Conversations)
	}
	assertLiveRowDescribesTheWork(t, conversationRow(t, listing, live), now.Add(-14*time.Second))
	if row := conversationRow(t, listing, derived); row.Role != orchestratorConversationRoleDerived || row.Resuming {
		t.Fatalf("expected the derived row marked derived and not resuming, got %+v", row)
	}
}

// assertLiveRowDescribesTheWork checks the row for the tracked conversation
// carries everything the choice is made on: that it is the live one, that it is
// what a launch would resume, and the when/how-big/where/how-it-opens the
// operator recognises it by.
func assertLiveRowDescribesTheWork(t *testing.T, row orchestratorConversation, written time.Time) {
	t.Helper()
	if row.Role != orchestratorConversationRoleLive || !row.Resuming {
		t.Fatalf("expected the live row marked live and resuming, got %+v", row)
	}
	if row.LastWrittenUnix != written.Unix() || row.SizeBytes == 0 {
		t.Fatalf("expected last-written and size on the row, got %+v", row)
	}
	if !strings.Contains(row.Excerpt, "carry the release through") {
		t.Fatalf("expected the row to carry an excerpt the operator can recognise, got %q", row.Excerpt)
	}
	if row.Folder != "/Users/me/orchestrators" {
		t.Fatalf("expected the folder read from the transcript's own cwd, got %q", row.Folder)
	}
}

// A conversation another orchestrator has a claim on is not on offer. Attaching
// one orchestrator to another's history is the crossing this area was fixed for,
// and a picker that lists it invites exactly that.
func TestConversationListingNeverOffersAnotherOrchestratorsConversation(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	other, err := app.CreateOrchestrator("stranger", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	theirDerived := orchestratorSessionID(other.ID)
	const theirLive = "9a9a9a9a-1111-4222-8333-444444444444"
	const mine = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	now := time.Now()
	stageTranscriptIn(t, "-orchestrators", theirDerived, "/Users/me/orchestrators", "theirs", now)
	stageTranscriptIn(t, "-orchestrators", theirLive, "/Users/me/orchestrators", "theirs too", now)
	stageTranscriptIn(t, "-orchestrators", mine, "/Users/me/orchestrators", "mine", now.Add(-time.Minute))
	writeLiveConversationRecord(t, other.ID, orchestratorLiveConversation{
		ConversationID: theirLive,
		LaunchID:       "their-launch",
	})

	listing, err := app.ListOrchestratorConversations(id)
	if err != nil {
		t.Fatalf("ListOrchestratorConversations failed: %v", err)
	}
	listed := listedConversationIDs(listing)
	for _, theirs := range []string{theirDerived, theirLive} {
		if _, offered := listed[theirs]; offered {
			t.Fatalf("offered another orchestrator's conversation %q: %+v", theirs, listing.Conversations)
		}
	}
	if _, offered := listed[mine]; !offered {
		t.Fatalf("expected an unowned conversation to be offered, got %+v", listing.Conversations)
	}
	// Left out, not hidden: a short list has to read as "these are yours" rather
	// than as a machine with nothing on it.
	if listing.OmittedNotMine != 2 {
		t.Fatalf("expected the two conversations left out to be counted, got %+v", listing)
	}
	if err := app.attachConversationForTest(id, theirLive); err == nil {
		t.Fatal("attaching another orchestrator's conversation was allowed")
	} else if !strings.Contains(err.Error(), other.ID) {
		t.Fatalf("expected the refusal to name the orchestrator that owns it, got %v", err)
	}
}

// AttachOrchestratorConversation is used for its error in the assertion above;
// this wrapper keeps that call site readable.
func (a *App) attachConversationForTest(id, conversationID string) error {
	_, err := a.AttachOrchestratorConversation(id, conversationID, 80, 24)
	return err
}

// The attach is the correction: the orchestrator restarts on the chosen
// conversation, and — the part that is easy to miss — the choice survives the
// next launch instead of being recomputed away.
func TestAttachingAConversationRestartsThereAndSurvivesTheNextLaunch(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	var launched []string
	app.deps.resolveOrchestratorLaunch = func(conversationID, _, _, _ string) (string, []string, error) {
		launched = append(launched, conversationID)
		return "claude-stub", nil, nil
	}

	id := createAndStartOrchestrator(t, app)
	const chosen = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	stageOrchestratorConversation(t, chosen)

	info, err := app.AttachOrchestratorConversation(id, chosen, 80, 24)
	if err != nil {
		t.Fatalf("AttachOrchestratorConversation failed: %v", err)
	}
	if info.Status != "running" {
		t.Fatalf("expected the orchestrator running on the attached conversation, got %+v", info)
	}
	if got := launched[len(launched)-1]; got != chosen {
		t.Fatalf("expected the session to start on %q, got %q", chosen, got)
	}
	if target := app.ResolveOrchestratorToReopen(); target.ConversationID != chosen {
		t.Fatalf("expected the attachment to survive the next launch, got %q", target.ConversationID)
	}
	assertAttachmentIsReported(t, app, id, chosen)

	// And it is clearable, or an attach would be a trap rather than a correction.
	if _, err := app.DetachOrchestratorConversation(id, 80, 24); err != nil {
		t.Fatalf("DetachOrchestratorConversation failed: %v", err)
	}
	if got := launched[len(launched)-1]; got != orchestratorSessionID(id) {
		t.Fatalf("expected detaching to fall back to the derived conversation, got %q", got)
	}
}

// assertAttachmentIsReported checks the listing says the orchestrator is on the
// conversation the operator attached, and labels that row as theirs -- an attach
// the surface does not reflect back is indistinguishable from one that failed.
func assertAttachmentIsReported(t *testing.T, app *App, id, chosen string) {
	t.Helper()
	listing, err := app.ListOrchestratorConversations(id)
	if err != nil {
		t.Fatalf("ListOrchestratorConversations failed: %v", err)
	}
	if listing.Attached != chosen || listing.ResumingSource != string(orchestratorConversationAttached) {
		t.Fatalf("expected the listing to report the attachment, got %+v", listing)
	}
	if row := conversationRow(t, listing, chosen); row.Role != orchestratorConversationRoleAttached {
		t.Fatalf("expected the attached row labelled as such, got %+v", row)
	}
}

// An attachment whose conversation has since been deleted must not silently
// become "the derived one, as if you never chose": the operator is told their
// choice could not be honoured.
func TestAnAttachedConversationThatIsGoneIsReportedNotIgnored(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const gone = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageOrchestratorConversation(t, orchestratorSessionID(id))
	if err := setAttachedOrchestratorConversation(app.deps.orchestratorOpenPath, id, gone); err != nil {
		t.Fatalf("attach: %v", err)
	}

	target := app.ResolveOrchestratorToReopen()
	if target.ConversationID != orchestratorSessionID(id) {
		t.Fatalf("expected the derived conversation, got %q", target.ConversationID)
	}
	if !strings.Contains(target.Notice, gone) || !strings.Contains(target.Notice, "no longer on disk") {
		t.Fatalf("expected the notice to name the attachment it could not honour, got %q", target.Notice)
	}
}

// A conversation started in some other directory is still this orchestrator's to
// resume, and its folder is read from the transcript rather than decoded from the
// directory name — which cannot round-trip a path that already contains a dash.
func TestConversationListingSpansFoldersAndReadsTheFolderFromTheTranscript(t *testing.T) {
	app, _, _ := openStateTestApp(t)
	defer app.shutdown(context.Background())

	id := createAndStartOrchestrator(t, app)
	const elsewhere = "0c01340d-65bd-4ed9-bb9e-91bdff59a6ec"
	stageTranscriptIn(t, "-Users-me-my-project", elsewhere, "/Users/me/my-project", "started somewhere else", time.Now())

	listing, err := app.ListOrchestratorConversations(id)
	if err != nil {
		t.Fatalf("ListOrchestratorConversations failed: %v", err)
	}
	row := conversationRow(t, listing, elsewhere)
	if row.Folder != "/Users/me/my-project" {
		t.Fatalf("expected the recorded cwd, got %q", row.Folder)
	}
}
