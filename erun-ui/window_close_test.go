package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"

	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
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

func TestBeforeCloseBlocksWithRunningWork(t *testing.T) {
	app := newTestAppForWindowClose(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	app.activity.Track(activitysvc.Entry{
		ID:          "deploy-1",
		Command:     "deploy",
		Tenant:      "acme",
		Environment: "prod",
		Status:      activitysvc.StatusRunning,
	})

	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Fatal("beforeClose must block the close while a deploy is running")
	}

	events := emits.events(uimodel.AppCloseGateEvent)
	if len(events) != 1 {
		t.Fatalf("expected one %s event, got %d", uimodel.AppCloseGateEvent, len(events))
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

func TestBeforeCloseIdleQueueUnchanged(t *testing.T) {
	app := newTestAppForWindowClose(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose must not block the close when nothing is running")
	}
	if events := emits.events(uimodel.AppCloseGateEvent); len(events) != 0 {
		t.Fatalf("expected no close-gate event on an idle queue, got %d", len(events))
	}
	if _, err := os.Stat(app.deps.windowStatePath); err != nil {
		t.Fatalf("expected window state to be saved: %v", err)
	}
}

func TestATabStillBeingEstablishedDoesNotBlockTheClose(t *testing.T) {
	for _, kind := range []string{
		activitysvc.GatedOpen,
		activitysvc.GatedAI,
		activitysvc.GatedContributeERun,
		activitysvc.GatedContributeAI,
	} {
		t.Run(kind, func(t *testing.T) {
			assertTabSetupDoesNotBlockTheClose(t, kind)
		})
	}
}

func assertTabSetupDoesNotBlockTheClose(t *testing.T, kind string) {
	t.Helper()
	app := newTestAppForWindowClose(t)
	t.Cleanup(app.activity.StopActionRunners)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uimodel.Selection{Tenant: "pw", Environment: "alpha"}
	held := &terminal.Session{Selection: selection}
	t.Cleanup(func() { held.SignalReady(nil) })

	if _, err := app.activity.EnqueueGatedSession(selection, kind, gatedStart(func(context.Context) (uimodel.StartSessionResult, *terminal.Session, error) {
		return uimodel.StartSessionResult{SessionID: 1, Selection: selection}, held, nil
	})); err != nil {
		t.Fatalf("EnqueueGatedSession: %v", err)
	}
	setup := app.FindActiveDeployForSelection(selection)
	if setup.Status != activitysvc.StatusRunning || setup.ActionKind != kind {
		t.Fatalf("expected the %s tab to be held running in setup, got %+v", kind, setup)
	}

	if gate := app.PrepareWindowClose(); gate.Blocked || len(gate.Running) != 0 {
		t.Fatalf("a %s tab still being established must not block the close: %+v", kind, gate)
	}
	if events := emits.events(uimodel.AppCloseGateEvent); len(events) != 0 {
		t.Fatalf("expected no close-gate event while only a tab is being established, got %d", len(events))
	}

	app.activity.Track(activitysvc.Entry{
		ID:          "deploy-1",
		Command:     "deploy",
		Tenant:      "pw",
		Environment: "alpha",
		Status:      activitysvc.StatusRunning,
	})
	gate := app.PrepareWindowClose()
	if !gate.Blocked || len(gate.Running) != 1 || gate.Running[0].ID != "deploy-1" {
		t.Fatalf("a running deploy must still block the close and be the only entry named: %+v", gate)
	}
}

func TestAnUnregisteredGatedSessionKindIsRefused(t *testing.T) {
	app := newTestAppForWindowClose(t)
	t.Cleanup(app.activity.StopActionRunners)
	started := false
	_, err := app.activity.EnqueueGatedSession(uimodel.Selection{Tenant: "pw", Environment: "alpha"}, "deploy", gatedStart(func(context.Context) (uimodel.StartSessionResult, *terminal.Session, error) {
		started = true
		return uimodel.StartSessionResult{}, nil, nil
	}))
	if err == nil || started {
		t.Fatalf("expected an unregistered kind to be refused before starting, err=%v started=%v", err, started)
	}
}

func TestConfirmWindowClosePersistsRecordThenQuits(t *testing.T) {
	app := newTestAppForWindowClose(t)
	quit := 0
	app.deps.quitApp = func() { quit++ }
	app.activity.Track(activitysvc.Entry{
		ID:          "release-1",
		Command:     "release",
		Tenant:      "acme",
		Environment: "prod",
		Status:      activitysvc.StatusRunning,
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
	var record activitysvc.InterruptedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("record was not valid JSON: %v", err)
	}
	if len(record.Entries) != 1 || record.Entries[0].ID != "release-1" {
		t.Fatalf("unexpected recorded entries: %+v", record.Entries)
	}

	// Must not re-prompt, or the dialog would reopen on its own confirm.
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose must not re-block after ConfirmWindowClose")
	}
	if events := emits.events(uimodel.AppCloseGateEvent); len(events) != 0 {
		t.Fatalf("beforeClose must not re-emit the gate after confirmation, got %d", len(events))
	}
}

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

func TestConfirmWindowCloseStillQuitsWhenRecordingFails(t *testing.T) {
	app := newTestAppForWindowClose(t)
	// A plain file as the parent makes MkdirAll fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	app.deps.interruptedActivityPath = filepath.Join(blocker, "interrupted-activity.json")
	quit := 0
	app.deps.quitApp = func() { quit++ }
	app.activity.Track(activitysvc.Entry{
		ID:      "deploy-1",
		Command: "deploy",
		Status:  activitysvc.StatusRunning,
	})

	if err := app.ConfirmWindowClose(); err == nil {
		t.Fatal("expected an error from the failed write")
	}
	if quit != 1 {
		t.Fatalf("expected quitApp to be called despite the write failure, got %d", quit)
	}
}
