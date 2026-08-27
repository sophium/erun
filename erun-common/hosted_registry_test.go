package eruncommon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHostedRegistryProbeTreatsUnauthorizedAsAvailable pins the rule the whole
// gate depends on: a token-authenticated registry answers an unauthenticated
// GET /v2/ with 401, so reading a non-2xx status as "down" would report every
// correctly configured registry as missing and refuse a choice that works.
func TestHostedRegistryProbeTreatsUnauthorizedAsAvailable(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		got := probeHostedRegistryAt(context.Background(), server.Client(), server.URL+"/v2/", HostedRegistryHost)
		server.Close()
		if !got.Available {
			t.Fatalf("status %d: expected the registry to read as available, got %+v", status, got)
		}
		if got.Err() != nil {
			t.Fatalf("status %d: an available registry must not produce an error, got %v", status, got.Err())
		}
	}
}

// TestHostedRegistryProbeNamesAnUnresolvableHost checks the dead-end rule: an
// unavailable status must name what it observed and carry a way forward.
func TestHostedRegistryProbeNamesAnUnresolvableHost(t *testing.T) {
	got := probeHostedRegistryAt(context.Background(), nil, "https://registry.invalid.erun-does-not-exist./v2/", HostedRegistryHost)
	if got.Available {
		t.Fatal("expected an unresolvable host to read as unavailable")
	}
	err := got.Err()
	if err == nil {
		t.Fatal("expected an unavailable status to produce an error")
	}
	if !strings.Contains(err.Error(), HostedRegistryHost) {
		t.Fatalf("the error must name the registry it is about, got %q", err)
	}
	if got.Recovery == "" {
		t.Fatalf("every unavailable status must carry a way forward, got %+v", got)
	}
}

// TestErunRegistryRefusesWhenUnreachable is the red-then-green case for #1494
// item 4: choosing the hosted registry must fail at the choice, naming the
// reason, rather than writing an environment whose every push resolves to a
// host that is not there.
func TestErunRegistryRefusesWhenUnreachable(t *testing.T) {
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		ProbeHostedRegistry: func(context.Context) HostedRegistryStatus {
			return HostedRegistryStatus{
				Host:     HostedRegistryHost,
				Reason:   "does not resolve",
				Recovery: "Choose a different registry instead.",
			}
		},
	}}
	registry, err := runner.resolveContainerRegistry(BootstrapInitParams{ErunRegistry: true}, "acme", "dev", "", "", true)
	if err == nil {
		t.Fatalf("expected --erun-registry to refuse an unreachable registry, got %q", registry)
	}
	if registry != "" {
		t.Fatalf("a refused choice must seed no registry, got %q", registry)
	}
	for _, want := range []string{HostedRegistryHost, "does not resolve", "Choose a different registry"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must contain %q, got %q", want, err)
		}
	}
}

// TestErunRegistryAcceptedWhenAvailable keeps the refusal from swallowing the
// working case.
func TestErunRegistryAcceptedWhenAvailable(t *testing.T) {
	runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
		ProbeHostedRegistry: func(context.Context) HostedRegistryStatus {
			return HostedRegistryStatus{Host: HostedRegistryHost, Available: true}
		},
	}}
	registry, err := runner.resolveContainerRegistry(BootstrapInitParams{ErunRegistry: true}, "acme", "dev", "", "", true)
	if err != nil {
		t.Fatalf("an available registry must be accepted: %v", err)
	}
	if want := HostedRegistryReference("acme"); registry != want {
		t.Fatalf("expected %q, got %q", want, registry)
	}
}
