package erunmcp

import (
	"context"
	"strings"
	"testing"
)

// exposePreviewRuntime is the smallest runtime the expose tool runs against
// with no project to resolve: the platform coordinates come in as the explicit
// servicesZone/platformNamespace override, which is the caller shape that
// needs no .erun/config.yaml at all.
func exposePreviewRuntime(t *testing.T) RuntimeConfig {
	t.Helper()
	return servicesTestRuntime(t, "frs", "dev")
}

func exposePreviewTrace(t *testing.T, runtime RuntimeConfig, input ExposeInput) string {
	t.Helper()
	input.Preview = true
	_, output, err := exposeTool(runtime)(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("exposeTool returned err: %v", err)
	}
	if output.Executed {
		t.Fatal("a preview reported executed work")
	}
	return strings.Join(output.Trace, "\n")
}

// TestExposeToolBackendServiceOverridesTheDerivedName is the MCP half of the
// reported gap: `backendService` is the only way an agent or script can name
// the in-namespace Service when the repo brought its own chart, and the input
// is worthless if it does not reach the plan. The plan is what the Ingress is
// rendered from, so the assertion is on the rendered manifest -- the resource
// a real run pipes to `kubectl apply` -- and on the trace line naming the
// routing target, not on the input struct having been read.
//
// Dropping the input (passing an empty BackendService through) silently falls
// back to the <tenant>-<service> derivation, which is exactly the reported
// failure: a hostname that resolves and an Ingress that 503s.
func TestExposeToolBackendServiceOverridesTheDerivedName(t *testing.T) {
	runtime := exposePreviewRuntime(t)

	override := exposePreviewTrace(t, runtime, ExposeInput{
		Tenant:            "frs",
		Environment:       "dev",
		Service:           "api",
		BackendService:    "pw-api",
		IP:                "203.0.113.10",
		ServicesZone:      "services.example.com",
		PlatformNamespace: "frs-prod",
	})
	if !strings.Contains(override, "expose: api.frs-dev.services.example.com -> service pw-api.frs-dev.svc:80") {
		t.Fatalf("backendService did not reach the resolved plan:\n%s", override)
	}
	if !strings.Contains(override, "name: pw-api") {
		t.Fatalf("the rendered Ingress does not route to the named backend:\n%s", override)
	}

	// The control: with no backendService the derivation still applies, so the
	// test above is reading the override and not the only value the tool can
	// ever produce.
	derived := exposePreviewTrace(t, runtime, ExposeInput{
		Tenant:            "frs",
		Environment:       "dev",
		Service:           "api",
		IP:                "203.0.113.10",
		ServicesZone:      "services.example.com",
		PlatformNamespace: "frs-prod",
	})
	if !strings.Contains(derived, "-> service frs-api.frs-dev.svc:80") {
		t.Fatalf("the derived <tenant>-<service> name is no longer the default:\n%s", derived)
	}
	if strings.Contains(derived, "name: pw-api") {
		t.Fatalf("a call that named no backend routed to one anyway:\n%s", derived)
	}
}
