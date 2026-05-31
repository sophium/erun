package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

// TestMaybeStopIdleEmitsSuccessAsNotificationNotPill locks the user-
// facing contract from issue #361: the auto-stop success line MUST
// land on app-notification (auto-dismissing toast), not on app-status
// (persistent titlebar pill). The "Stopping..." in-flight line still
// belongs on app-status — its busy spinner is a long-running status
// indicator that benefits from the persistent surface.
func TestMaybeStopIdleEmitsSuccessAsNotificationNotPill(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"team-stop": {
				Name:               "team-stop",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "dev-stop",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"team-stop/dev-stop": {
				Name:               "dev-stop",
				RepoPath:           projectRoot,
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Remote:             true,
			},
		},
	}
	stopped := make(chan string, 1)
	emits := newCapturedEmits()
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		loadIdleStatus: func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error) {
			return eruncommon.EnvironmentIdleStatus{
				ManagedCloud: true,
				StopEligible: true,
				Policy: eruncommon.EnvironmentIdlePolicy{
					Timeout: 5 * time.Minute,
				},
				Markers: []eruncommon.EnvironmentIdleMarker{
					{Name: "working-hours", Idle: true},
					{Name: eruncommon.ActivityKindSSH, Idle: true},
					{Name: eruncommon.ActivityKindMCP, Idle: true},
					{Name: eruncommon.ActivityKindCLI, Idle: true},
					{Name: eruncommon.ActivityKindCodex, Idle: true},
				},
			}, nil
		},
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			stopped <- name
			return eruncommon.CloudContextStatus{
				CloudContextConfig: eruncommon.CloudContextConfig{Name: name},
				Status:             eruncommon.CloudContextStatusStopped,
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())
	app.setCloudContextStatusInCache("cloud-ctx", eruncommon.CloudContextStatusRunning)

	// First poll arms the grace warning; the stop must not fire yet.
	t0 := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	app.SetNowFunc(func() time.Time { return t0 })
	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "team-stop", Environment: "dev-stop"}); err != nil {
		t.Fatalf("LoadIdleStatus failed: %v", err)
	}
	select {
	case got := <-stopped:
		t.Fatalf("stop fired during grace window: %s", got)
	case <-time.After(50 * time.Millisecond):
	}

	// Advance past the grace window so the next poll fires the real
	// stop and emits the success notification.
	app.SetNowFunc(func() time.Time { return t0.Add(10 * time.Minute) })
	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "team-stop", Environment: "dev-stop"}); err != nil {
		t.Fatalf("second LoadIdleStatus failed: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cloud context stop")
	}

	// Find the success notification among any earlier warnings. The
	// grace-arm path emits an "Auto-stop pending..." warning notification
	// on the first eligible poll, which precedes the success info.
	successPayload, err := waitForAppNotificationByKind(emits, "info", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if successPayload.Kind != "info" {
		t.Fatalf("success notification kind = %q, want info", successPayload.Kind)
	}
	if got := successPayload.Message; !strings.HasPrefix(got, "Stopped idle cloud context cluster-cloud") {
		t.Fatalf("success notification message = %q, want prefix %q", got, "Stopped idle cloud context cluster-cloud")
	}

	// The "Stopping..." busy line must still ride on app-status —
	// that's the long-running indicator. Regressing it back into a
	// notification would lose the busy spinner.
	statusEvents := emits.events(appStatusEvent)
	if len(statusEvents) == 0 {
		t.Fatal("expected at least one app-status emit for the in-flight busy line")
	}
	sawStopping := false
	for _, evt := range statusEvents {
		payload, ok := evt.(appStatusPayload)
		if !ok {
			continue
		}
		if payload.Busy && payload.Message == "Stopping idle cloud context cluster-cloud..." {
			sawStopping = true
		}
		if payload.Message == "Stopped idle cloud context cluster-cloud." {
			t.Fatalf("success line leaked onto app-status (persistent pill): %+v", payload)
		}
	}
	if !sawStopping {
		t.Fatalf("expected Stopping... busy line on app-status, got %+v", statusEvents)
	}
}

func waitForAppNotification(emits *capturedEmits, timeout time.Duration) (appNotificationPayload, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := emits.events(appNotificationEvent)
		for _, evt := range events {
			payload, ok := evt.(appNotificationPayload)
			if !ok {
				continue
			}
			return payload, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return appNotificationPayload{}, fmt.Errorf("timed out waiting for app-notification emit")
}

// waitForAppNotificationByKind returns the first notification whose
// Kind matches the requested string. Lets a test that arms the
// grace-period warning (kind="warning") still find the later
// success (kind="info") without false-matching the earlier one.
func waitForAppNotificationByKind(emits *capturedEmits, kind string, timeout time.Duration) (appNotificationPayload, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := emits.events(appNotificationEvent)
		for _, evt := range events {
			payload, ok := evt.(appNotificationPayload)
			if !ok {
				continue
			}
			if payload.Kind == kind {
				return payload, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return appNotificationPayload{}, fmt.Errorf("timed out waiting for app-notification emit with kind=%q", kind)
}
