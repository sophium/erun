package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestWhip(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"whip", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_targets_configured", func(t *testing.T) {
		// No environments and no orchestrators configured at all: the report
		// has nothing to name, and the command still exits clean.
		setup := env.New(t)
		result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_no_targets_configured", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_one_named_environment_not_open_reports_call_failed", func(t *testing.T) {
		// Pinned to the 26100 range (erun-integration/AGENTS.md's "pin a high
		// port range") rather than the plain SeedTenantEnv default, or the probe
		// below can land on whatever a developer's live erun session already
		// holds on the default 17000 range and read as a stale port-forward
		// instead of the "nobody has this open" case the scenario means to test.
		//
		// This harness has no desktop identity to mint a bearer with, so the
		// whip tool call itself fails before it ever reaches a pod -- reported
		// as a call failure (erun-common's WhipReasonCallFailed), never folded
		// into "not alive" the way it used to be (erun#1709): the tool call
		// erroring out is never the same claim as the tool reporting a dead
		// session, which comes back as a *successful* result.
		skipIfPortsBusy(t, 26100)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_one_named_environment_not_open_reports_call_failed", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_whips_every_configured_environment_and_orchestrator", func(t *testing.T) {
		// With neither --tenant nor --environment, whip fans out over every
		// configured environment (across every tenant) plus every persisted
		// orchestrator. Orchestrators are always reported unreachable from
		// this transport -- a CLI process has no channel into a desktop-held
		// PTY -- independent of whether any environment edge is reachable.
		// Pinned ports for the same reason as the scenario above.
		skipIfPortsBusy(t, 26100, 26200)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "other", "staging", 26200)
		seedOrchestrator(t, setup, "eng-1", "Eng One")
		result := erun.Run(t, []string{"whip", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_whips_every_configured_environment_and_orchestrator", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_json_output", func(t *testing.T) {
		// Pinned ports for the same reason as the scenarios above.
		skipIfPortsBusy(t, 26100)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		seedOrchestrator(t, setup, "eng-1", "Eng One")
		result := erun.Run(t, []string{"whip", "--dry-run", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "whip/dry_run_json_output", normalize.Apply(result.Combined))
	})

	t.Run("real_run_forwards_the_resolved_target", func(t *testing.T) {
		// erun#1709: `erun whip` used to call the "whip" MCP tool with only
		// {"preview": ...}, relying entirely on the pod defaulting to its own
		// bound context. This proves the call now restates the tenant/
		// environment this host already resolved to reach the edge, through a
		// real (fake) edge rather than the dry-run path above, which never
		// reaches a live tool result to decode.
		whipEdgeLocalPort := 26500
		skipIfPortsBusy(t, whipEdgeLocalPort)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", whipEdgeLocalPort)
		fixture.SeedDesktopIdentity(t, setup)
		edge := &fakeMCPEdge{Results: map[string]string{"tools/call": `{"content":[{"type":"text","text":"whip"}],` +
			`"structuredContent":{"candidate":{"Kind":"environment","ID":"team/dev","Name":"team/dev","Reachable":true,"Alive":true},"decision":1,"reason":"nudge","pushed":true}}`}}
		edge.start(t, whipEdgeLocalPort)

		result := erun.Run(t, []string{"whip", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}

		call := edge.requestFor(t, "tools/call")
		if call.Tool != "whip" {
			t.Fatalf("the environment was asked for %q, want whip", call.Tool)
		}
		assertToolArgumentsNameTarget(t, call, "team", "dev")
	})

	t.Run("dry_run_only_tenant_given_refuses", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"whip", "--tenant", "team", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with only --tenant given, got 0: %s", result.Combined)
		}
		golden.Equal(t, "whip/dry_run_only_tenant_given_refuses", normalize.Apply(result.Combined))
	})
}

// seedOrchestrator appends a persisted orchestrator definition to the isolated
// root config, mirroring what the desktop writes to ~/.erun/config.yaml (see
// erun-common/config.go's OrchestratorConfig) without exercising the desktop
// UI that normally creates one.
func seedOrchestrator(t testing.TB, setup env.Setup, id, name string) {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read root config %s: %v", path, err)
	}
	extra := "orchestrators:\n  - id: " + id + "\n    name: " + name + "\n"
	if err := os.WriteFile(path, append(existing, []byte(extra)...), 0o644); err != nil {
		t.Fatalf("write root config %s: %v", path, err)
	}
}
