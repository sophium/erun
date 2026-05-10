package eruncommon

import "testing"

func TestServicePortUsesLowerServicePort(t *testing.T) {
	if got := ServicePort(0); got != LowerServicePort {
		t.Fatalf("expected base service port %d, got %d", LowerServicePort, got)
	}
}

func TestMCPServicePortUsesOffsetFromLowerServicePort(t *testing.T) {
	want := LowerServicePort + MCPServicePortOffset
	if MCPServicePort != want {
		t.Fatalf("expected MCP service port %d, got %d", want, MCPServicePort)
	}
}

func TestAPIServicePortUsesOffsetFromLowerServicePort(t *testing.T) {
	want := LowerServicePort + APIServicePortOffset
	if APIServicePort != want {
		t.Fatalf("expected API service port %d, got %d", want, APIServicePort)
	}
}

func TestResolveEnvironmentLocalPortsUsesSortedTenantEnvironmentOrder(t *testing.T) {
	store := listStore{
		openStore: openStore{
			tenantConfigs: map[string]TenantConfig{
				"tenant-b": {Name: "tenant-b"},
				"tenant-a": {Name: "tenant-a"},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: "prod"}, {Name: "dev"}},
			"tenant-b": {{Name: "stage"}},
		},
	}

	ports, err := ResolveEnvironmentLocalPorts(store, "tenant-b", "stage")
	if err != nil {
		t.Fatalf("ResolveEnvironmentLocalPorts failed: %v", err)
	}

	if ports.RangeStart != 17200 || ports.RangeEnd != 17299 {
		t.Fatalf("unexpected local port range: %+v", ports)
	}
	if ports.MCP != 17200 || ports.API != 17233 || ports.SSH != 17222 {
		t.Fatalf("unexpected local service ports: %+v", ports)
	}
}
