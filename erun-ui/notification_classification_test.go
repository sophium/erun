package main

import (
	"context"
	"errors"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestEnsureMCPAvailableEmitsClassifiedWarning locks the reclassification of
// the local-MCP-unreachable notice: it used to go out on the unclassified
// emitAppStatus busy channel, and now goes out as a classified, env-tagged
// warning so the message centre can count and review it like any other
// notification.
func TestEnsureMCPAvailableEmitsClassifiedWarning(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{
		emitFn: emits.fn(),
		deps: erunUIDeps{
			ensureMCP: func(context.Context, eruncommon.OpenResult) error {
				return nil
			},
			canReachMCPEndpoint: func(int) bool {
				return false
			},
		},
	}
	result := eruncommon.OpenResult{Tenant: "frs", EnvConfig: eruncommon.EnvConfig{Name: "dev"}}

	if err := app.ensureMCPAvailable(context.Background(), result); err != nil {
		t.Fatalf("ensureMCPAvailable returned an error: %v", err)
	}

	if got := emits.events(appStatusEvent); len(got) != 0 {
		t.Fatalf("expected no unclassified app-status emit, got %+v", got)
	}
	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != notificationSourceMCPUnreachable {
		t.Fatalf("source = %q, want %q", payload.Source, notificationSourceMCPUnreachable)
	}
	if payload.Message == "" {
		t.Fatal("expected a non-empty message")
	}
}

// TestEnsureMCPAvailableEmitsNothingWhenReachable locks the flip side: a
// reachable endpoint must not post the warning at all.
func TestEnsureMCPAvailableEmitsNothingWhenReachable(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{
		emitFn: emits.fn(),
		deps: erunUIDeps{
			ensureMCP: func(context.Context, eruncommon.OpenResult) error {
				t.Fatal("did not expect ensureMCP to run when already reachable")
				return nil
			},
			canReachMCPEndpoint: func(int) bool {
				return true
			},
		},
	}
	result := eruncommon.OpenResult{Tenant: "frs", EnvConfig: eruncommon.EnvConfig{Name: "dev"}}

	if err := app.ensureMCPAvailable(context.Background(), result); err != nil {
		t.Fatalf("ensureMCPAvailable returned an error: %v", err)
	}
	if got := emits.events(appNotificationEvent); len(got) != 0 {
		t.Fatalf("expected no notification when already reachable, got %+v", got)
	}
}

// TestSurfaceCredentialRefreshFailureEmitsClassifiedError locks the
// reclassification of the host-credential-refresh failure notice from the
// unclassified emitAppStatus channel to a classified, env-tagged error.
func TestSurfaceCredentialRefreshFailureEmitsClassifiedError(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	selection := uiSelection{Tenant: "frs", Environment: "dev"}

	app.surfaceCredentialRefreshFailure(selection, errors.New("token expired"), nil)

	if got := emits.events(appStatusEvent); len(got) != 0 {
		t.Fatalf("expected no unclassified app-status emit, got %+v", got)
	}
	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "error" {
		t.Fatalf("kind = %q, want error", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != notificationSourceCredentialRefreshFailed {
		t.Fatalf("source = %q, want %q", payload.Source, notificationSourceCredentialRefreshFailed)
	}
}

// TestSurfaceCredentialRefreshFailureNotifiesOnlyOnce locks the existing
// dedup contract (the *notified flag) surviving the reclassification.
func TestSurfaceCredentialRefreshFailureNotifiesOnlyOnce(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}
	selection := uiSelection{Tenant: "frs", Environment: "dev"}
	notified := false

	app.surfaceCredentialRefreshFailure(selection, errors.New("token expired"), &notified)
	app.surfaceCredentialRefreshFailure(selection, errors.New("token expired"), &notified)

	if got := emits.events(appNotificationEvent); len(got) != 1 {
		t.Fatalf("expected exactly one notification across two calls, got %+v", got)
	}
}
