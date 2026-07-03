package main

import (
	"testing"
)

// TestEmitAppNotificationDropsEmptyMessage locks the contract that the
// helper does not push transparent toasts at the frontend. An empty
// message would still pop a notification slot if it slipped through;
// the early return keeps the channel quiet.
func TestEmitAppNotificationDropsEmptyMessage(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	for _, message := range []string{"", "   ", "\t\n"} {
		app.emitAppNotification("info", message)
	}
	if got := emits.events(appNotificationEvent); len(got) != 0 {
		t.Fatalf("expected no emits for empty messages, got %+v", got)
	}
}

// TestEmitAppNotificationCarriesKindAndMessage exercises the happy
// path: kind + trimmed-style message reach the wire intact so the
// frontend can route the payload through showNotification with the
// matching AppNotification['kind'].
func TestEmitAppNotificationCarriesKindAndMessage(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	app.emitAppNotification("info", "Stopped idle cloud context cluster-cloud.")
	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one emit, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
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

// Note: TestMaybeStopIdleEmitsSuccessAsNotificationNotPill used to
// verify that the desktop emitted a "Stopped idle cloud context X."
// notification (and not a persistent pill) when it fired the
// auto-stop itself. The auto-stop firing moved to the in-pod monitor
// when the auto-stop grace period was unified across desktop and
// in-pod monitor, so the desktop no longer emits this
// notification on its own. The post-fire UX is exercised at the
// transport boundary by the integration suite (which verifies
// `erun activity record-stop` writes the history entry) and by the
// Playwright spec (which mocks the MCP response and renders the
// History tab).
