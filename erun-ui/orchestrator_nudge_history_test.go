package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// nudgeOrchestratorOnce drives one pacing nudge into id's live session via the
// real reconciler path (sendOrchestratorPacingNudge), the same way an
// automatic pacer nudge happens in production, so these tests exercise the
// same write-through persistence a real nudge triggers rather than poking the
// struct directly.
func nudgeOrchestratorOnce(t *testing.T, app *App, id string) {
	t.Helper()
	app.mu.Lock()
	session := app.orchestrators[id]
	app.mu.Unlock()
	if session == nil {
		t.Fatalf("expected a live session for %q", id)
	}
	app.sendOrchestratorPacingNudge(id, session.serial, time.Now(), time.Minute, orchestratorPacingSignalIdle, "", false)
}

// orchestratorAutoNudgeSnapshot reads a live session's cumulative auto-nudge
// fields under lock.
func orchestratorAutoNudgeSnapshot(app *App, id string) (count int, lastAtUnix int64) {
	app.mu.Lock()
	defer app.mu.Unlock()
	session := app.orchestrators[id]
	if session == nil {
		return 0, 0
	}
	return session.pacingAutoNudgeCount, session.pacingLastAutoNudgeAtUnix
}

// TestOrchestratorNudgeHistorySurvivesStopThenStart is the issue's first Go
// validation criterion: a session with a non-zero pacingAutoNudgeCount that is
// torn down and reattached (Stop then Start again, the same "reattach" shape
// a desktop restart's boot-restore path uses) reports the same count.
func TestOrchestratorNudgeHistorySurvivesStopThenStart(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	nudgeOrchestratorOnce(t, app, created.ID)
	nudgeOrchestratorOnce(t, app, created.ID)

	autoCount, lastAutoAt := orchestratorAutoNudgeSnapshot(app, created.ID)
	if autoCount != 2 || lastAutoAt == 0 {
		t.Fatalf("expected the nudges to be recorded before stop, got count=%d lastAt=%d", autoCount, lastAutoAt)
	}

	// Stop tears down the in-memory session -- the same "gone" state a
	// desktop restart leaves behind -- so a restore afterward is a genuine
	// reattach, not a read of the same still-live struct.
	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	restarted, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator (reattach) failed: %v", err)
	}
	if restarted.AutoNudgeCount != 2 || restarted.LastAutoNudgeAtUnix != lastAutoAt {
		t.Fatalf("expected the reattached session to report the same history, got autoNudgeCount=%d lastAutoNudgeAtUnix=%d (want 2, %d)",
			restarted.AutoNudgeCount, restarted.LastAutoNudgeAtUnix, lastAutoAt)
	}
	if restarted.NudgeHistoryUnreadable {
		t.Fatal("expected a valid persisted record to not be reported as unreadable")
	}
}

// TestOrchestratorNudgeHistoryFreshOrchestratorStartsAtZero is the issue's
// paired criterion: a freshly created orchestrator (no prior record for its
// id) reports zero, so "never nudged" stays true for one that genuinely never
// was.
func TestOrchestratorNudgeHistoryFreshOrchestratorStartsAtZero(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("fresh", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if started.AutoNudgeCount != 0 || started.WhipCount != 0 || started.LastCappedAtUnix != 0 {
		t.Fatalf("expected a brand new orchestrator to report no history, got %+v", started)
	}
}

// TestDeleteOrchestratorClearsNudgeHistory locks in the id-reuse hazard: the
// orchestrator id is a name-derived slug (uniqueOrchestratorID), not a uuid,
// so deleting "agent" and creating a new orchestrator also named "agent"
// reuses the same id. Without clearing the persisted record on delete, the
// new orchestrator would inherit a stranger's nudge history.
func TestDeleteOrchestratorClearsNudgeHistory(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	nudgeOrchestratorOnce(t, app, created.ID)

	if err := app.DeleteOrchestrator(created.ID); err != nil {
		t.Fatalf("DeleteOrchestrator failed: %v", err)
	}

	recreated, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("recreate CreateOrchestrator failed: %v", err)
	}
	if recreated.ID != created.ID {
		t.Fatalf("expected the id to be reused (uniqueOrchestratorID is name-derived), got %q want %q", recreated.ID, created.ID)
	}
	started, err := app.StartOrchestrator(recreated.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator (recreated) failed: %v", err)
	}
	if started.AutoNudgeCount != 0 {
		t.Fatalf("expected the recreated orchestrator's reused id to start with no history, got autoNudgeCount=%d", started.AutoNudgeCount)
	}
}

// TestListOrchestratorsReportsHistoryForStoppedOrchestrator pins the
// complementary gap: ListOrchestrators' "stopped" branch used to report an
// unconditional zero pacing snapshot for any orchestrator with no live
// session, which would have re-created the exact "Not nudged" defect this
// issue closes every time the operator merely stopped (not restarted) a
// nudged orchestrator.
func TestListOrchestratorsReportsHistoryForStoppedOrchestrator(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	nudgeOrchestratorOnce(t, app, created.ID)
	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	list := app.ListOrchestrators()
	if len(list) != 1 {
		t.Fatalf("expected exactly one orchestrator listed, got %d", len(list))
	}
	if list[0].Status != "stopped" {
		t.Fatalf("expected the orchestrator to be listed as stopped, got %q", list[0].Status)
	}
	if list[0].AutoNudgeCount != 1 {
		t.Fatalf("expected the stopped orchestrator to still report its nudge history, got autoNudgeCount=%d", list[0].AutoNudgeCount)
	}
}

// TestOrchestratorNudgeHistoryUnreadableFileIsReportedNotSilentlyZeroed pins
// the "unknown, not a confident zero" contract: a persisted file that exists
// but fails to parse must not be read back as "never nudged" — it must be
// distinguishable.
func TestOrchestratorNudgeHistoryUnreadableFileIsReportedNotSilentlyZeroed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	path := filepath.Join(home, "nudge-history.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupt history file: %v", err)
	}

	app := NewApp(erunUIDeps{
		store:                        newOrchestratorStubStore(t.TempDir()),
		orchestratorNudgeHistoryPath: path,
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if !started.NudgeHistoryUnreadable {
		t.Fatal("expected an unreadable persisted history to be reported as such")
	}
	if started.AutoNudgeCount != 0 || started.WhipCount != 0 {
		t.Fatalf("expected the unreadable case to still report zero counts (unverified, not asserted), got %+v", started)
	}
}

// TestWriteOrchestratorNudgeHistoryEntryRefusesToClobberAnUnreadableFile pins
// the write-side safeguard: if the existing file cannot be parsed, writing one
// orchestrator's update must not replace it with a set containing only that
// one entry, which would silently destroy every other orchestrator's history.
func TestWriteOrchestratorNudgeHistoryEntryRefusesToClobberAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nudge-history.json")
	original := []byte("{not valid json")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed corrupt history file: %v", err)
	}

	err := writeOrchestratorNudgeHistoryEntry(path, orchestratorNudgeHistoryEntry{OrchestratorID: "agent", AutoNudgeCount: 5})
	if err == nil {
		t.Fatal("expected the write to refuse rather than clobber an unreadable file")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back history file: %v", readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("expected the unreadable file to be left untouched, got %q", string(data))
	}
}

// TestOrchestratorNudgeHistoryEntryPersistsAtomically is a light integration
// check that writeOrchestratorNudgeHistoryEntry actually round-trips through
// eruncommon.WriteFileAtomic (temp file + rename) rather than a plain
// truncating write: the file exists, parses, and reads back exactly what was
// written.
func TestOrchestratorNudgeHistoryEntryPersistsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "nudge-history.json")

	entry := orchestratorNudgeHistoryEntry{
		OrchestratorID:      "agent",
		AutoNudgeCount:      3,
		LastAutoNudgeAtUnix: 111,
		WhipCount:           1,
		LastWhipAtUnix:      222,
		LastCappedAtUnix:    333,
	}
	if err := writeOrchestratorNudgeHistoryEntry(path, entry); err != nil {
		t.Fatalf("writeOrchestratorNudgeHistoryEntry failed: %v", err)
	}

	got, found, unreadable := orchestratorNudgeHistoryFor(path, "agent")
	if unreadable || !found {
		t.Fatalf("expected the entry to be found and readable, found=%v unreadable=%v", found, unreadable)
	}
	if got != entry {
		t.Fatalf("expected the round-tripped entry to match, got %+v want %+v", got, entry)
	}

	// A second orchestrator's entry must coexist rather than replace the first.
	other := orchestratorNudgeHistoryEntry{OrchestratorID: "other", WhipCount: 9}
	if err := writeOrchestratorNudgeHistoryEntry(path, other); err != nil {
		t.Fatalf("second writeOrchestratorNudgeHistoryEntry failed: %v", err)
	}
	if got, found, _ := orchestratorNudgeHistoryFor(path, "agent"); !found || got != entry {
		t.Fatalf("expected the first orchestrator's entry to survive a second orchestrator's write, got found=%v entry=%+v", found, got)
	}
	if got, found, _ := orchestratorNudgeHistoryFor(path, "other"); !found || got != other {
		t.Fatalf("expected the second orchestrator's entry to be persisted too, got found=%v entry=%+v", found, got)
	}
}
