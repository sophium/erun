package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestExpose(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"expose", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// With a platform block and an env, expose resolves the public hostname
		// and traces the full plan: the per-env wildcard A record upsert into
		// the services zone and the Host-routing Ingress apply. No side effects.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_cross_cluster", func(t *testing.T) {
		// The platform env (frs-prod) that owns PowerDNS is on a different
		// cluster than the target env (team-dev). The per-env wildcard DNS write
		// must exec against the platform env's own kube context, while the Ingress
		// applies against the target env's context — the two must not collapse to
		// one (a cross-cluster misroute). The platform env is seeded with a
		// distinct context to lock that the DNS exec uses it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedTenantEnvWithContext(t, setup, "frs", "prod", "platform-context")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run_cross_cluster", normalize.Apply(result.Combined))
	})

	t.Run("requires_platform_config", func(t *testing.T) {
		// expose only makes sense for a platform deployment; without a platform
		// block in .erun/config.yaml it fails with an actionable error rather
		// than resolving a hostname under an unknown zone.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a platform block, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_platform_config", normalize.Apply(result.Combined))
	})

	t.Run("requires_platform_env", func(t *testing.T) {
		// platform.env locates the PowerDNS pod's namespace for the per-env
		// wildcard DNS write. A platform block with a base domain but no env would
		// otherwise produce a `kubectl -n "" exec` that silently misroutes, so
		// expose fails fast with an actionable error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without platform.env, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_platform_env", normalize.Apply(result.Combined))
	})

	t.Run("requires_ip", func(t *testing.T) {
		// The per-env wildcard record needs a target IP (the env's ingress IP);
		// omitting --ip fails clearly instead of writing an empty record.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without --ip, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_ip", normalize.Apply(result.Combined))
	})
}
