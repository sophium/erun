package main

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestLoadHostedRegistryDelegatesToDeps(t *testing.T) {
	app := &App{
		deps: erunUIDeps{
			loadHostedRegistry: func(context.Context) uiHostedRegistryStatus {
				return uiHostedRegistryStatus{
					Host:     config.HostedRegistryHost,
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

// The headless Playwright harness cannot stub the probe's outbound HTTP, so
// the override must win.
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

// A canceled context fails the probe without depending on DNS.
func TestLoadHostedRegistryDefaultMapsAnUnreachableProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status := loadHostedRegistry(ctx)
	if status.Available {
		t.Fatal("expected a canceled probe context to report unavailable")
	}
	if status.Host != config.HostedRegistryHost {
		t.Fatalf("host = %q, want %q", status.Host, config.HostedRegistryHost)
	}
	if status.Reason == "" {
		t.Fatal("expected a reason naming why the registry is unavailable")
	}
	if status.Recovery == "" {
		t.Fatal("expected a recovery action, not just a reason")
	}
}
