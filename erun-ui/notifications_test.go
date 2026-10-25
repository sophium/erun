package main

import (
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestEmitAppNotificationDropsEmptyMessage(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	for _, message := range []string{"", "   ", "\t\n"} {
		app.emitAppNotification("info", message)
	}
	if got := emits.events(uimodel.AppNotificationEvent); len(got) != 0 {
		t.Fatalf("expected no emits for empty messages, got %+v", got)
	}
}

func TestEmitAppNotificationCarriesKindAndMessage(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	app.emitAppNotification("info", "Stopped idle cloud context cluster-cloud.")
	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one emit, got %+v", events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "info" {
		t.Fatalf("kind = %q, want info", payload.Kind)
	}
	if payload.Message != "Stopped idle cloud context cluster-cloud." {
		t.Fatalf("unexpected message: %q", payload.Message)
	}
}
