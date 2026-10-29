package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
)

// nudgeOrchestratorOnce nudges through the real reconciler path so persistence runs.
func nudgeOrchestratorOnce(t *testing.T, app *testApp, id string) {
	t.Helper()
	_, unlock := lockTerminalForTest(app)
	session := app.orchestrators[id]
	unlock()
	if session == nil {
		t.Fatalf("expected a live session for %q", id)
	}
	app.sendOrchestratorPacingNudge(id, session.serial, time.Now(), time.Minute, orchestratorPacingSignalIdle, "", false)
}

func orchestratorAutoNudgeSnapshot(app *testApp, id string) (count int, lastAtUnix int64) {
	_, unlock := lockTerminalForTest(app)
	defer unlock()
	session := app.orchestrators[id]
	if session == nil {
		return 0, 0
	}
	return session.pacingAutoNudgeCount, session.pacingLastAutoNudgeAtUnix
}

func TestOrchestratorNudgeHistorySurvivesStopThenStart(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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

	// Stop drops the in-memory session, so the restore is a genuine reattach.
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

func TestOrchestratorNudgeHistoryFreshOrchestratorStartsAtZero(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("fresh", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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

// Orchestrator ids are name-derived, so a new "agent" reuses a deleted one's id.
func TestDeleteOrchestratorClearsNudgeHistory(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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

	recreated, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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

func TestListOrchestratorsReportsHistoryForStoppedOrchestrator(t *testing.T) {
	orchestratorPacingNudgeSettle = 0
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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

func TestOrchestratorNudgeHistoryUnreadableFileIsReportedNotSilentlyZeroed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	path := filepath.Join(home, "nudge-history.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupt history file: %v", err)
	}

	app := newTestApp(testDeps{
		store:                        newOrchestratorStubStore(t.TempDir()),
		orchestratorNudgeHistoryPath: path,
		startTerminal: func(StartParams) (PTY, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchcmd.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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
