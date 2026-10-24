package erunmcp

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/environment"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func TestEnvironmentToolResolvesAgainstServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]config.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs:    map[string]config.EnvConfig{"tenant-a/dev": {Name: "dev", Type: config.EnvironmentTypeLocalAgent}},
		},
	}

	_, result, err := environment.EnvironmentTool(runtime)(context.Background(), nil, environment.EnvironmentInput{Preview: true})
	if err != nil {
		t.Fatalf("resolve environment read model: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment.Name != "dev" {
		t.Fatalf("expected the server context to fill the target, got tenant=%q environment=%+v", result.Tenant, result.Environment)
	}
	if result.Health != nil {
		t.Fatalf("preview must skip the live deploy diagnosis, got %+v", result.Health)
	}
	if result.State == lifecycle.EnvironmentLifecycleUnknown {
		t.Fatalf("a resolvable non-managed environment with a real idle read should not report unknown")
	}
}

// In-pod config has no localportrangestart; the injected ports must win over the default.
func TestEnvironmentToolPrefersInjectedPortsOverADefaultedRange(t *testing.T) {
	isolateLeaseCache(t)
	t.Setenv("ERUN_TENANT", "tenant-a")
	t.Setenv("ERUN_ENVIRONMENT", "dev")
	t.Setenv("ERUN_MCP_PORT", "17200")
	t.Setenv("ERUN_SSHD_PORT", "17222")
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]config.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs:    map[string]config.EnvConfig{"tenant-a/dev": {Name: "dev", Type: config.EnvironmentTypeLocalAgent}},
			envsByTenant:  map[string][]config.EnvConfig{"tenant-a": {{Name: "dev", Type: config.EnvironmentTypeLocalAgent}}},
		},
	}

	_, result, err := environment.EnvironmentTool(runtime)(context.Background(), nil, environment.EnvironmentInput{Preview: true})
	if err != nil {
		t.Fatalf("resolve environment read model: %v", err)
	}
	ports := result.Environment.LocalPorts
	if ports.MCP != 17200 || ports.SSH != 17222 {
		t.Fatalf("expected the injected ports, got %+v", ports)
	}
	if ports.RangeStart != 17200 || ports.RangeEnd != 17299 {
		t.Fatalf("expected the injected range, got %+v", ports)
	}
	if ports.API != 17233 {
		t.Fatalf("expected the API port within the injected range, got %+v", ports)
	}
	if result.Environment.APIURL != "http://127.0.0.1:17233" {
		t.Fatalf("expected apiUrl to follow the injected range, got %q", result.Environment.APIURL)
	}
}

// Unobservable power state must read unknown, not stopped or idle.
func TestEnvironmentToolManagedCloudWithoutObservedPowerStateReadsUnknown(t *testing.T) {
	isolateLeaseCache(t)
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]config.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs: map[string]config.EnvConfig{
				"tenant-a/dev": {Name: "dev", Type: config.EnvironmentTypeRuntime, ManagedCloud: true},
			},
		},
	}

	_, result, err := environment.EnvironmentTool(runtime)(context.Background(), nil, environment.EnvironmentInput{Preview: true})
	if err != nil {
		t.Fatalf("resolve environment read model: %v", err)
	}
	if result.State != lifecycle.EnvironmentLifecycleUnknown {
		t.Fatalf("an unobserved managed-cloud power state must read as unknown, got %s", result.State)
	}
	if result.State == lifecycle.EnvironmentLifecycleStopped || result.State == lifecycle.EnvironmentLifecycleIdle {
		t.Fatalf("an unobserved signal must never resolve to a real state; got %s", result.State)
	}
}
