package eruncommon

import "testing"

// TestLocalPortsForResultPrefersInjectedRuntimePorts covers the open path: the
// pod's own CLI resolves its environment's edge from the same derivation, so a
// process that carries the chart-injected ports must resolve that environment
// to them rather than to the defaulted block.
func TestLocalPortsForResultPrefersInjectedRuntimePorts(t *testing.T) {
	t.Setenv("ERUN_TENANT", "tenant-a")
	t.Setenv("ERUN_ENVIRONMENT", "dev")
	t.Setenv("ERUN_MCP_PORT", "17200")
	t.Setenv("ERUN_SSHD_PORT", "17222")

	result := OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
		LocalPorts:  EnvironmentLocalPortsFromRangeStart(LowerServicePort),
	}
	if got := MCPPortForResult(result); got != 17200 {
		t.Fatalf("expected the injected mcp port, got %d", got)
	}
	if got := SSHLocalPortForResult(result); got != 17222 {
		t.Fatalf("expected the injected ssh port, got %d", got)
	}
	if got := APIPortForResult(result); got != 17233 {
		t.Fatalf("expected the api port within the injected range, got %d", got)
	}
}

// TestOverlayInjectedRuntimeLocalPorts covers the in-pod port overlay: an
// environment's own runtime pod is the only process whose injected ERUN_*_PORT
// values are authoritative for it, and the on-disk projection that pod reads
// carries no localportrangestart to derive the same block from.
func TestOverlayInjectedRuntimeLocalPorts(t *testing.T) {
	injectedEnv := map[string]string{
		"ERUN_TENANT":      "tenant-a",
		"ERUN_ENVIRONMENT": "dev",
		"ERUN_MCP_PORT":    "17200",
		"ERUN_SSHD_PORT":   "17222",
	}
	for _, tc := range []struct {
		name        string
		env         map[string]string
		tenant      string
		environment string
		ports       EnvironmentLocalPorts
		want        EnvironmentLocalPorts
	}{
		{
			name:        "injected ports replace the defaulted 17000 block",
			env:         injectedEnv,
			tenant:      "tenant-a",
			environment: "dev",
			ports:       EnvironmentLocalPortsFromRangeStart(LowerServicePort),
			want:        EnvironmentLocalPorts{RangeStart: 17200, RangeEnd: 17299, MCP: 17200, API: 17233, SSH: 17222, ContributeApp: 17250},
		},
		{
			name:        "off-pod the config-derived block stands",
			env:         map[string]string{"ERUN_MCP_PORT": "17200", "ERUN_SSHD_PORT": "17222"},
			tenant:      "tenant-a",
			environment: "dev",
			ports:       EnvironmentLocalPortsFromRangeStart(LowerServicePort),
			want:        EnvironmentLocalPortsFromRangeStart(LowerServicePort),
		},
		{
			name:        "another environment's injected ports are not this environment's",
			env:         injectedEnv,
			tenant:      "tenant-a",
			environment: "prod",
			ports:       EnvironmentLocalPortsFromRangeStart(18000),
			want:        EnvironmentLocalPortsFromRangeStart(18000),
		},
		{
			name:        "a missing ssh port keeps the config-resolved one",
			env:         map[string]string{"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "dev", "ERUN_MCP_PORT": "17200"},
			tenant:      "tenant-a",
			environment: "dev",
			ports:       EnvironmentLocalPorts{RangeStart: 17000, RangeEnd: 17099, MCP: 17000, API: 17033, SSH: 17999},
			want:        EnvironmentLocalPorts{RangeStart: 17200, RangeEnd: 17299, MCP: 17200, API: 17233, SSH: 17999, ContributeApp: 17250},
		},
		{
			name:        "an injected mcp port that is not a range start is not used",
			env:         map[string]string{"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "dev", "ERUN_MCP_PORT": "17233"},
			tenant:      "tenant-a",
			environment: "dev",
			ports:       EnvironmentLocalPortsFromRangeStart(LowerServicePort),
			want:        EnvironmentLocalPortsFromRangeStart(LowerServicePort),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := overlayInjectedRuntimeLocalPorts(tc.ports, func(key string) string { return tc.env[key] }, tc.tenant, tc.environment)
			if got != tc.want {
				t.Fatalf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}
