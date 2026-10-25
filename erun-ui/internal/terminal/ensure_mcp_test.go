package terminal

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestEnsureMCPAvailableEmitsClassifiedWarning(t *testing.T) {
	emits := newCapturedEmits()
	app := newTestService(Deps{
		Emit: emits.fn(),
		EnsureMCP: func(context.Context, config.OpenResult) error {
			return nil
		},
		CanReachMCPEndpoint: func(int) bool {
			return false
		},
	})
	result := config.OpenResult{Tenant: "frs", EnvConfig: config.EnvConfig{Name: "dev"}}

	if err := app.EnsureMCPAvailable(context.Background(), result); err != nil {
		t.Fatalf("ensureMCPAvailable returned an error: %v", err)
	}

	if got := emits.events(uimodel.AppStatusEvent); len(got) != 0 {
		t.Fatalf("expected no unclassified app-status emit, got %+v", got)
	}
	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != SourceMCPUnreachable {
		t.Fatalf("source = %q, want %q", payload.Source, SourceMCPUnreachable)
	}
	if payload.Message == "" {
		t.Fatal("expected a non-empty message")
	}
}

func TestEnsureMCPAvailableEmitsNothingWhenReachable(t *testing.T) {
	emits := newCapturedEmits()
	app := newTestService(Deps{
		Emit: emits.fn(),
		EnsureMCP: func(context.Context, config.OpenResult) error {
			t.Fatal("did not expect ensureMCP to run when already reachable")
			return nil
		},
		CanReachMCPEndpoint: func(int) bool {
			return true
		},
	})
	result := config.OpenResult{Tenant: "frs", EnvConfig: config.EnvConfig{Name: "dev"}}

	if err := app.EnsureMCPAvailable(context.Background(), result); err != nil {
		t.Fatalf("ensureMCPAvailable returned an error: %v", err)
	}
	if got := emits.events(uimodel.AppNotificationEvent); len(got) != 0 {
		t.Fatalf("expected no notification when already reachable, got %+v", got)
	}
}
