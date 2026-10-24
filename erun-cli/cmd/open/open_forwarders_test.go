package open

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// The dry-run integration binary cannot fail a forward with the runtime present, so this test owns the contract.
func TestActivateForwardersBestEffort(t *testing.T) {
	var out bytes.Buffer
	var sshdCalled, mcpCalled, apiCalled bool
	runner := &resolvedOpenRunner{
		ctx: core.Context{Logger: core.NewLoggerWithWriters(0, &out, &out)},
		result: config.OpenResult{
			Tenant:      "team",
			Environment: "dev",
			EnvConfig:   config.EnvConfig{Name: "dev", SSHD: config.SSHDConfig{Enabled: true}},
		},
		activateSSHD: func(core.Context, config.OpenResult) error { sshdCalled = true; return fmt.Errorf("sshd boom") },
		activateMCP:  func(core.Context, config.OpenResult) error { mcpCalled = true; return fmt.Errorf("mcp boom") },
		activateAPI:  func(core.Context, config.OpenResult) error { apiCalled = true; return fmt.Errorf("api boom") },
	}

	err := runner.activateForwarders()

	if !sshdCalled || !mcpCalled || !apiCalled {
		t.Fatalf("every forwarder must be attempted despite failures: sshd=%v mcp=%v api=%v", sshdCalled, mcpCalled, apiCalled)
	}
	got := out.String()
	for _, want := range []string{
		"SSH port-forward unavailable",
		"MCP port-forward unavailable",
		"API port-forward unavailable",
		"mcp boom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning output missing %q; got:\n%s", want, got)
		}
	}
	if err == nil {
		t.Fatal("expected activateForwarders to still report every failure via its returned error")
	}
	for _, want := range []string{"sshd boom", "mcp boom", "api boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("returned error missing %q; got: %v", want, err)
		}
	}
}
