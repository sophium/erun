package erunmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/expose"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// exposePreviewRuntime passes servicesZone/platformNamespace, so no project is needed.
func exposePreviewRuntime(t *testing.T) runtimecfg.RuntimeConfig {
	t.Helper()
	return servicesTestRuntime(t, "frs", "dev")
}

func exposePreviewTrace(t *testing.T, runtime runtimecfg.RuntimeConfig, input expose.ExposeInput) string {
	t.Helper()
	input.Preview = true
	_, output, err := expose.ExposeTool(runtime)(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("exposeTool returned err: %v", err)
	}
	if output.Executed {
		t.Fatal("a preview reported executed work")
	}
	return strings.Join(output.Trace, "\n")
}

// backendService must reach the rendered Ingress; dropping it falls back to
// <tenant>-<service>, which 503s for repo-native charts.
func TestExposeToolBackendServiceOverridesTheDerivedName(t *testing.T) {
	runtime := exposePreviewRuntime(t)

	override := exposePreviewTrace(t, runtime, expose.ExposeInput{
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

	// Control: without backendService the derivation still applies.
	derived := exposePreviewTrace(t, runtime, expose.ExposeInput{
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
