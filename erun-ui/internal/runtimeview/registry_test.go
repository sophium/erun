package runtimeview

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-common/base/config/containerregistry"
)

func TestLoadHostedRegistryDelegatesToDeps(t *testing.T) {
	service := newTestService(Deps{
		LoadHostedRegistry: func(context.Context) HostedRegistryStatus {
			return HostedRegistryStatus{
				Host:     containerregistry.HostedRegistryHost,
				Reason:   "does not resolve",
				Recovery: "Choose a different registry instead.",
			}
		},
	})
	status, err := service.LoadHostedRegistry()
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

// A canceled context fails the probe without depending on DNS.
func TestLoadHostedRegistryDefaultMapsAnUnreachableProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status := ProbeHostedRegistry(ctx)
	if status.Available {
		t.Fatal("expected a canceled probe context to report unavailable")
	}
	if status.Host != containerregistry.HostedRegistryHost {
		t.Fatalf("host = %q, want %q", status.Host, containerregistry.HostedRegistryHost)
	}
	if status.Reason == "" {
		t.Fatal("expected a reason naming why the registry is unavailable")
	}
	if status.Recovery == "" {
		t.Fatal("expected a recovery action, not just a reason")
	}
}
