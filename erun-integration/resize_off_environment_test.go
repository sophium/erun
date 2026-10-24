package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// resize rolls the pod locally, but off-environment its occupancy check must read
// the target environment's leases through that environment's MCP edge.
const resizeEdgeLocalPort = 26600

const (
	resizeHeldLeasePayload = `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev","held":[` +
		`{"id":"exec_job_attach","name":"exec_job_attach","exclusive":true,"scope":"worktree",` +
		`"expiresAt":"2099-01-01T00:00:00Z","holder":{"orchestrator":"eng-42"}}]}}`
	resizeNoLeasePayload = `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev","held":[]}}`
)

func TestResizeOffEnvironment(t *testing.T) {
	t.Run("dry_run_held_lease_refuses_and_names_holder", testResizeOffEnvironmentDryRunHeldLeaseRefusesAndNamesHolder)
	t.Run("dry_run_held_lease_with_override_proceeds", testResizeOffEnvironmentDryRunHeldLeaseWithOverrideProceeds)
	t.Run("dry_run_no_lease_traces_will_roll", testResizeOffEnvironmentDryRunNoLeaseTracesWillRoll)
	t.Run("real_run_held_lease_refuses_before_any_deploy_call", testResizeOffEnvironmentRealRunHeldLeaseRefusesBeforeAnyDeployCall)
}

func testResizeOffEnvironmentDryRunHeldLeaseRefusesAndNamesHolder(t *testing.T) {
	// A remote target's leases live only in its own store; the caller's local one is empty.
	skipIfPortsBusy(t, resizeEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": resizeHeldLeasePayload}}
	edge.start(t, resizeEdgeLocalPort)

	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit while the environment's own lease is held, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "orchestrator eng-42") {
		t.Fatalf("expected the refusal to name the holder, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "exec_job_attach") {
		t.Fatalf("expected the refusal to name the held lease, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/off_environment_dry_run_held_lease_refuses_and_names_holder", normalize.Apply(result.Combined))

	call := edge.requestFor(t, "tools/call")
	if call.Tool != "activity_lease_list" {
		t.Fatalf("the environment was asked for %q, want activity_lease_list", call.Tool)
	}
	// The occupancy check is a read even when forced through under --dry-run, so it must
	// carry the idle-probe header.
	if !call.IdleProbe {
		t.Errorf("expected the resize occupancy check to carry the idle-probe header")
	}
	assertToolArgumentsNameTarget(t, call, "team", "dev")
}

func testResizeOffEnvironmentDryRunHeldLeaseWithOverrideProceeds(t *testing.T) {
	skipIfPortsBusy(t, resizeEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": resizeHeldLeasePayload}}
	edge.start(t, resizeEdgeLocalPort)

	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--override-lease", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "overriding") {
		t.Fatalf("expected the trace to record the override, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/off_environment_dry_run_held_lease_with_override_proceeds", normalize.Apply(result.Combined))
}

func testResizeOffEnvironmentDryRunNoLeaseTracesWillRoll(t *testing.T) {
	skipIfPortsBusy(t, resizeEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": resizeNoLeasePayload}}
	edge.start(t, resizeEdgeLocalPort)

	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "will roll the runtime pod") {
		t.Fatalf("expected the plan to proceed to the roll trace, got:\n%s", result.Combined)
	}
	golden.Equal(t, "resize/off_environment_dry_run_no_lease_traces_will_roll", normalize.Apply(result.Combined))
}

func testResizeOffEnvironmentRealRunHeldLeaseRefusesBeforeAnyDeployCall(t *testing.T) {
	// No chart is seeded, so the scenario would fail differently if the refusal were not first.
	skipIfPortsBusy(t, resizeEdgeLocalPort)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
	fixture.SeedDesktopIdentity(t, setup)
	edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": resizeHeldLeasePayload}}
	edge.start(t, resizeEdgeLocalPort)

	result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit while the environment's own lease is held, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "orchestrator eng-42") {
		t.Fatalf("expected the refusal to name the holder, got:\n%s", result.Combined)
	}
	assertEnvConfigUnchanged(t, setup, "team", "dev")
}
