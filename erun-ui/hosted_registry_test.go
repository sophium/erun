package main

import (
	"context"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestLoadHostedRegistryDelegatesToDeps locks down that the Wails-exported
// method is a thin pass-through to the injected probe, mirroring how
// LoadClusterRegistry defers to deps.loadClusterRegistry.
func TestLoadHostedRegistryDelegatesToDeps(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			loadHostedRegistry: func(context.Context) uiHostedRegistryStatus {
				return uiHostedRegistryStatus{
					Host:     eruncommon.HostedRegistryHost,
					Reason:   "does not resolve",
					Recovery: "Choose a different registry instead.",
				}
			},
		},
	}
	status, err := app.LoadHostedRegistry()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Available {
		t.Fatal("expected the unavailable status to pass through unchanged")
	}
	if status.Reason != "does not resolve" {
		t.Fatalf("reason = %q", status.Reason)
	}
}

// TestHostedRegistryProbeOverridePinsTheAnswer locks down the seam the
// headless Playwright harness depends on: page.route can stub the Wails
// method a spec calls, but not the outbound HTTP call the real probe would
// make underneath it, so an unstubbed spec needs a deterministic answer with
// no network access at all. ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE must win
// regardless of what a real probe would have found.
func TestHostedRegistryProbeOverridePinsTheAnswer(t *testing.T) {
	t.Setenv("ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE", "1")
	deps := withDefaultRuntimeDeps(erunUIDeps{})

	status := deps.loadHostedRegistry(context.Background())
	if !status.Available {
		t.Fatalf("expected override=1 to report available, got %+v", status)
	}
	if status.Reason != "" || status.Recovery != "" {
		t.Fatalf("expected no reason/recovery on an available status, got %+v", status)
	}
}

// TestHostedRegistryProbeOverrideUnsetFallsBackToRealProbe pins that the
// override is opt-in: with no env var set, the default dep still resolves to
// a real probe rather than a fixed answer (production behavior unchanged).
func TestHostedRegistryProbeOverrideUnsetFallsBackToRealProbe(t *testing.T) {
	deps := withDefaultRuntimeDeps(erunUIDeps{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status := deps.loadHostedRegistry(ctx)
	if status.Available {
		t.Fatal("expected the real probe to run and report unavailable for a canceled context")
	}
	if status.Reason == "" {
		t.Fatal("expected the real probe's reason to be set, not the override's")
	}
}

// TestLoadHostedRegistryDefaultMapsAnUnreachableProbe locks down the negative
// this whole change exists for: an unreachable hosted registry must resolve
// to available=false with a reason and recovery a caller can show, not to a
// Go error the App method would otherwise have to swallow or misreport as
// "available". An already-canceled context forces the probe to fail without
// depending on real DNS behavior, so the test is deterministic regardless of
// whether this host happens to resolve on the machine running it.
func TestLoadHostedRegistryDefaultMapsAnUnreachableProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status := loadHostedRegistry(ctx)
	if status.Available {
		t.Fatal("expected a canceled probe context to report unavailable")
	}
	if status.Host != eruncommon.HostedRegistryHost {
		t.Fatalf("host = %q, want %q", status.Host, eruncommon.HostedRegistryHost)
	}
	if status.Reason == "" {
		t.Fatal("expected a reason naming why the registry is unavailable")
	}
	if status.Recovery == "" {
		t.Fatal("expected a recovery action, not just a reason")
	}
}
