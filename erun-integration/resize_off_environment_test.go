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

// resize is a hybrid command: it always rolls the pod locally (kubectl/helm
// against the caller's own kubeconfig), but its occupancy check has to answer
// about the *target* environment's own activity leases, which off-environment
// only the environment's own MCP edge can see. These scenarios cover that
// half, mirroring TestActivityLeaseOffEnvironment/TestIdleOffEnvironment in
// environment_half_scenarios_test.go; TestResize (resize_test.go) covers the
// in-pod half via inEnvironment(...).
const resizeEdgeLocalPort = 26600

func TestResizeOffEnvironment(t *testing.T) {
	heldLeasePayload := `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev","held":[` +
		`{"id":"exec_job_attach","name":"exec_job_attach","exclusive":true,"scope":"worktree",` +
		`"expiresAt":"2099-01-01T00:00:00Z","holder":{"orchestrator":"eng-42"}}]}}`
	noLeasePayload := `{"content":[{"type":"text","text":"held"}],` +
		`"structuredContent":{"tenant":"team","environment":"dev","held":[]}}`

	t.Run("dry_run_held_lease_refuses_and_names_holder", func(t *testing.T) {
		// A resize invoked from off the target environment must consult that
		// environment's own lease store, not the caller's local one, which for
		// a remote target is always empty.
		skipIfPortsBusy(t, resizeEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": heldLeasePayload}}
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
		// The occupancy check only reads the environment's leases, so it must
		// carry the idle-probe header like any other diagnostic read -- even
		// though the caller forces the real call through under --dry-run (see
		// resizeActivityLeaseLoader), it stays a read, never new activity.
		if !call.IdleProbe {
			t.Errorf("expected the resize occupancy check to carry the idle-probe header")
		}
		assertToolArgumentsNameTarget(t, call, "team", "dev")
	})

	t.Run("dry_run_held_lease_with_override_proceeds", func(t *testing.T) {
		skipIfPortsBusy(t, resizeEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": heldLeasePayload}}
		edge.start(t, resizeEdgeLocalPort)

		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--override-lease", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "overriding") {
			t.Fatalf("expected the trace to record the override, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/off_environment_dry_run_held_lease_with_override_proceeds", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_lease_traces_will_roll", func(t *testing.T) {
		// The control case: with nothing held in the environment's own store,
		// the dispatched check must let the plan through rather than refuse.
		skipIfPortsBusy(t, resizeEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": noLeasePayload}}
		edge.start(t, resizeEdgeLocalPort)

		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "will roll the runtime pod") {
			t.Fatalf("expected the plan to proceed to the roll trace, got:\n%s", result.Combined)
		}
		golden.Equal(t, "resize/off_environment_dry_run_no_lease_traces_will_roll", normalize.Apply(result.Combined))
	})

	t.Run("real_run_held_lease_refuses_before_any_deploy_call", func(t *testing.T) {
		// The occupancy check runs before the config is persisted or the pod is
		// rolled, so a real (non-dry-run) resize against a held environment must
		// refuse having touched neither -- no devops chart or kubectl/helm stub
		// is seeded, so this scenario would fail for a different reason (a
		// missing chart) if the refusal did not happen first.
		skipIfPortsBusy(t, resizeEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", resizeEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{ToolResults: map[string]string{"activity_lease_list": heldLeasePayload}}
		edge.start(t, resizeEdgeLocalPort)

		result := erun.Run(t, []string{"resize", "--tenant", "team", "--environment", "dev", "--cpu", "6"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit while the environment's own lease is held, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "orchestrator eng-42") {
			t.Fatalf("expected the refusal to name the holder, got:\n%s", result.Combined)
		}
		assertEnvConfigUnchanged(t, setup, "team", "dev")
	})
}
