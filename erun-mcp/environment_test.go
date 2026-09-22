package erunmcp

import (
	"context"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The environment read model composes list/idle/doctor; what matters here is
// the transport contract (server-context defaulting, preview skipping the
// live deploy diagnosis) -- see erun-common's own coverage of
// ResolveEnvironmentLifecycleState for the state machine itself.
func TestEnvironmentToolResolvesAgainstServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]eruncommon.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs:    map[string]eruncommon.EnvConfig{"tenant-a/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent}},
		},
	}

	_, result, err := environmentTool(runtime)(context.Background(), nil, EnvironmentInput{Preview: true})
	if err != nil {
		t.Fatalf("resolve environment read model: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment.Name != "dev" {
		t.Fatalf("expected the server context to fill the target, got tenant=%q environment=%+v", result.Tenant, result.Environment)
	}
	if result.Health != nil {
		t.Fatalf("preview must skip the live deploy diagnosis, got %+v", result.Health)
	}
	if result.State == eruncommon.EnvironmentLifecycleUnknown {
		t.Fatalf("a resolvable non-managed environment with a real idle read should not report unknown")
	}
}

// TestEnvironmentToolPrefersInjectedPortsOverADefaultedRange is the in-pod
// case: the config the runtime chart projects carries no localportrangestart,
// so the port allocation falls back to the 17000 block -- while the chart
// injected the ports the pod is actually listening on into this very process.
// The read model must report those, not the default.
func TestEnvironmentToolPrefersInjectedPortsOverADefaultedRange(t *testing.T) {
	isolateLeaseCache(t)
	t.Setenv("ERUN_TENANT", "tenant-a")
	t.Setenv("ERUN_ENVIRONMENT", "dev")
	t.Setenv("ERUN_MCP_PORT", "17200")
	t.Setenv("ERUN_SSHD_PORT", "17222")
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]eruncommon.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs:    map[string]eruncommon.EnvConfig{"tenant-a/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent}},
			envsByTenant:  map[string][]eruncommon.EnvConfig{"tenant-a": {{Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent}}},
		},
	}

	_, result, err := environmentTool(runtime)(context.Background(), nil, EnvironmentInput{Preview: true})
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

// TestEnvironmentToolManagedCloudWithoutObservedPowerStateReadsUnknown is the
// mandatory negative case at the transport level: a managed-cloud
// environment with no matching cloud context configured (so its power state
// was never, and can never be, observed by this call) must read as unknown,
// not as stopped or idle.
func TestEnvironmentToolManagedCloudWithoutObservedPowerStateReadsUnknown(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store: listToolStore{
			tenantConfigs: map[string]eruncommon.TenantConfig{"tenant-a": {Name: "tenant-a"}},
			envConfigs: map[string]eruncommon.EnvConfig{
				"tenant-a/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeRuntime, ManagedCloud: true},
			},
		},
	}

	_, result, err := environmentTool(runtime)(context.Background(), nil, EnvironmentInput{Preview: true})
	if err != nil {
		t.Fatalf("resolve environment read model: %v", err)
	}
	if result.State != eruncommon.EnvironmentLifecycleUnknown {
		t.Fatalf("an unobserved managed-cloud power state must read as unknown, got %s", result.State)
	}
	if result.State == eruncommon.EnvironmentLifecycleStopped || result.State == eruncommon.EnvironmentLifecycleIdle {
		t.Fatalf("an unobserved signal must never resolve to a real state; got %s", result.State)
	}
}
