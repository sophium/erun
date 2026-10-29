package main

import (
	"context"
	"testing"
)

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
