package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestAppForWindowClose(t *testing.T) *App {
	t.Helper()
	app := NewApp(erunUIDeps{
		windowStatePath:         filepath.Join(t.TempDir(), "window-state.json"),
		interruptedActivityPath: filepath.Join(t.TempDir(), "interrupted-activity.json"),
		windowMaximised:         func(context.Context) bool { return false },
	})
	return app
}

// TestBeforeCloseBlocksWithRunningWork pins the actual "Fix" erun#1214 asks
// for: a running deploy must block the window from closing and name itself
// to the operator, instead of the previous unconditional `return false`.
func TestBeforeCloseBlocksWithRunningWork(t *testing.T) {
	app := newTestAppForWindowClose(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	app.activityQueue.start(activityQueueEntry{
		ID:          "deploy-1",
		Command:     "deploy",
		Tenant:      "acme",
		Environment: "prod",
		Status:      activityQueueStatusRunning,
	})

	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Fatal("beforeClose must block the close while a deploy is running")
	}

	events := emits.events(appCloseGateEvent)
	if len(events) != 1 {
		t.Fatalf("expected one %s event, got %d", appCloseGateEvent, len(events))
	}
	gate, ok := events[0].(uiCloseGate)
	if !ok {
		t.Fatalf("unexpected event payload type %T", events[0])
	}
	if !gate.Blocked || len(gate.Running) != 1 || gate.Running[0].ID != "deploy-1" {
		t.Fatalf("unexpected gate payload: %+v", gate)
	}

	if _, err := os.Stat(app.deps.windowStatePath); err == nil {
		t.Fatal("window state must not be saved when the close is blocked")
	}
}

// TestBeforeCloseIdleQueueUnchanged is the no-op path from the issue's own
// validation list: closing with an idle queue must not prompt.
func TestBeforeCloseIdleQueueUnchanged(t *testing.T) {
	app := newTestAppForWindowClose(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose must not block the close when nothing is running")
	}
	if events := emits.events(appCloseGateEvent); len(events) != 0 {
		t.Fatalf("expected no close-gate event on an idle queue, got %d", len(events))
	}
	if _, err := os.Stat(app.deps.windowStatePath); err != nil {
		t.Fatalf("expected window state to be saved: %v", err)
	}
}

// TestConfirmWindowClosePersistsRecordThenQuits pins the second half of the
// fix: confirming the close records what was running before anything is
// killed, then quits without re-prompting.
func TestConfirmWindowClosePersistsRecordThenQuits(t *testing.T) {
	app := newTestAppForWindowClose(t)
	quit := 0
	app.deps.quitApp = func() { quit++ }
	app.activityQueue.start(activityQueueEntry{
		ID:          "release-1",
		Command:     "release",
		Tenant:      "acme",
		Environment: "prod",
		Status:      activityQueueStatusRunning,
	})

	if err := app.ConfirmWindowClose(); err != nil {
		t.Fatalf("ConfirmWindowClose failed: %v", err)
	}
	if quit != 1 {
		t.Fatalf("expected quitApp to be called once, got %d", quit)
	}

	data, err := os.ReadFile(app.deps.interruptedActivityPath)
	if err != nil {
		t.Fatalf("expected an interrupted-activity record: %v", err)
	}
	var record interruptedActivityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("record was not valid JSON: %v", err)
	}
	if len(record.Entries) != 1 || record.Entries[0].ID != "release-1" {
		t.Fatalf("unexpected recorded entries: %+v", record.Entries)
	}

	// The confirmation must survive into the Quit-driven beforeClose pass
	// without re-prompting, or the dialog would reopen on its own confirm.
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose must not re-block after ConfirmWindowClose")
	}
	if events := emits.events(appCloseGateEvent); len(events) != 0 {
		t.Fatalf("beforeClose must not re-emit the gate after confirmation, got %d", len(events))
	}
}

// TestConfirmWindowCloseSkipsRecordWhenNothingIsRunning covers the race where
// the running work finishes between the prompt and the confirm click: no
// stale record should be written.
func TestConfirmWindowCloseSkipsRecordWhenNothingIsRunning(t *testing.T) {
	app := newTestAppForWindowClose(t)
	app.deps.quitApp = func() {}

	if err := app.ConfirmWindowClose(); err != nil {
		t.Fatalf("ConfirmWindowClose failed: %v", err)
	}
	if _, err := os.Stat(app.deps.interruptedActivityPath); !os.IsNotExist(err) {
		t.Fatalf("expected no interrupted-activity record, stat err=%v", err)
	}
}

// TestConfirmWindowCloseStillQuitsWhenRecordingFails: the operator already
// confirmed closing despite the running work, so a disk error persisting the
// courtesy record must not trap them in the window — it surfaces as a
// returned error but the quit still happens.
func TestConfirmWindowCloseStillQuitsWhenRecordingFails(t *testing.T) {
	app := newTestAppForWindowClose(t)
	// Point the record path's "directory" at a plain file, so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	app.deps.interruptedActivityPath = filepath.Join(blocker, "interrupted-activity.json")
	quit := 0
	app.deps.quitApp = func() { quit++ }
	app.activityQueue.start(activityQueueEntry{
		ID:      "deploy-1",
		Command: "deploy",
		Status:  activityQueueStatusRunning,
	})

	if err := app.ConfirmWindowClose(); err == nil {
		t.Fatal("expected an error from the failed write")
	}
	if quit != 1 {
		t.Fatalf("expected quitApp to be called despite the write failure, got %d", quit)
	}
}
