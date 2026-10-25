package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam/bootstrap"
	"github.com/sophium/erun/erun-common/command/setup"
)

// A token-authenticated registry answers an unauthenticated GET /v2/ with 401.
func TestHostedRegistryProbeTreatsUnauthorizedAsAvailable(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		got := setup.ProbeHostedRegistryAt(context.Background(), server.Client(), server.URL+"/v2/", config.HostedRegistryHost)
		server.Close()
		if !got.Available {
			t.Fatalf("status %d: expected the registry to read as available, got %+v", status, got)
		}
		if got.Err() != nil {
			t.Fatalf("status %d: an available registry must not produce an error, got %v", status, got.Err())
		}
	}
}

func TestHostedRegistryProbeNamesAnUnresolvableHost(t *testing.T) {
	got := setup.ProbeHostedRegistryAt(context.Background(), nil, "https://registry.invalid.erun-does-not-exist./v2/", config.HostedRegistryHost)
	if got.Available {
		t.Fatal("expected an unresolvable host to read as unavailable")
	}
	err := got.Err()
	if err == nil {
		t.Fatal("expected an unavailable status to produce an error")
	}
	if !strings.Contains(err.Error(), config.HostedRegistryHost) {
		t.Fatalf("the error must name the registry it is about, got %q", err)
	}
	if got.Recovery == "" {
		t.Fatalf("every unavailable status must carry a way forward, got %+v", got)
	}
}

func TestErunRegistryRefusesWhenUnreachable(t *testing.T) {
	runner := setup.BootstrapRunner{BootstrapInitDependencies: setup.BootstrapInitDependencies{
		ProbeHostedRegistry: func(context.Context) setup.HostedRegistryStatus {
			return setup.HostedRegistryStatus{
				Host:     config.HostedRegistryHost,
				Reason:   "does not resolve",
				Recovery: "Choose a different registry instead.",
			}
		},
	}}
	registry, err := runner.ResolveContainerRegistry(bootstrap.BootstrapInitParams{ErunRegistry: true}, "acme", "dev", "", "", true)
	if err == nil {
		t.Fatalf("expected --erun-registry to refuse an unreachable registry, got %q", registry)
	}
	if registry != "" {
		t.Fatalf("a refused choice must seed no registry, got %q", registry)
	}
	for _, want := range []string{config.HostedRegistryHost, "does not resolve", "Choose a different registry"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must contain %q, got %q", want, err)
		}
	}
}

func TestErunRegistryAcceptedWhenAvailable(t *testing.T) {
	runner := setup.BootstrapRunner{BootstrapInitDependencies: setup.BootstrapInitDependencies{
		ProbeHostedRegistry: func(context.Context) setup.HostedRegistryStatus {
			return setup.HostedRegistryStatus{Host: config.HostedRegistryHost, Available: true}
		},
	}}
	registry, err := runner.ResolveContainerRegistry(bootstrap.BootstrapInitParams{ErunRegistry: true}, "acme", "dev", "", "", true)
	if err != nil {
		t.Fatalf("an available registry must be accepted: %v", err)
	}
	if want := config.HostedRegistryReference("acme"); registry != want {
		t.Fatalf("expected %q, got %q", want, registry)
	}
}
