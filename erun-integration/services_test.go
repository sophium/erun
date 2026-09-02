package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestServices(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"services", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// Both planned kubectl reads are traced, in order, with no fetch and no
		// output -- the dry-run contract for a read-only command.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"services", "team", "dev", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("requires_environment", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"services", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unconfigured environment, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "services/requires_environment", normalize.Apply(result.Combined))
	})

	// real_run_reports_ground_truth_exposure drives the live path with a
	// stubbed kubectl: one Service already routed to by a real expose-*
	// Ingress (reported with its real hostname/scheme, not a name guess), one
	// following the <tenant>-<service> convention but not yet exposed
	// (reported with the label erun expose would need), and one that follows
	// neither (reported as not exposable, matching a repo that brought its
	// own chart -- issue #1906's point 2).
	t.Run("real_run_reports_ground_truth_exposure", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, map[string]string{
			"service": `{"items":[
				{"metadata":{"name":"team-api"},"spec":{"ports":[{"name":"http","port":80,"protocol":"TCP"}]}},
				{"metadata":{"name":"team-worker"},"spec":{"ports":[{"port":8080}]}},
				{"metadata":{"name":"validation-agent-backend-api"},"spec":{"ports":[{"port":3000}]}}
			]}`,
			"ingress": `{"items":[
				{"metadata":{"name":"expose-api"},"spec":{"rules":[{"host":"api.team-dev.services.test","http":{"paths":[{"backend":{"service":{"name":"team-api"}}}]}}]}}
			]}`,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/real_run_reports_ground_truth_exposure", normalize.Apply(result.Combined))
	})

	t.Run("real_run_json_output", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, map[string]string{
			"service": `{"items":[{"metadata":{"name":"team-api"},"spec":{"ports":[{"name":"http","port":80,"protocol":"TCP"}]}}]}`,
			"ingress": `{"items":[]}`,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services", "team", "dev", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/real_run_json_output", normalize.Apply(result.Combined))
	})
}
