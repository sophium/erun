package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestUnexpose(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"unexpose", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "unexpose/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// Happy path: a platform block plus an env yields a complete unexpose
		// plan (the delete-rrset command against the platform's PowerDNS pod)
		// with no side effects.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"unexpose", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "unexpose/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_platform_override", func(t *testing.T) {
		// --services-zone/--platform-namespace let a sourceless caller (the
		// hosted delete Job, with no git checkout) run unexpose without a
		// project, mirroring expose's own override.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"unexpose", "team", "dev",
			"--services-zone", "services.erunpaas.com",
			"--platform-namespace", "frs-prod",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "unexpose/dry_run_with_platform_override", normalize.Apply(result.Combined))
	})

	t.Run("requires_platform_config", func(t *testing.T) {
		// unexpose only makes sense for a platform deployment; without a
		// platform block it fails with an actionable error rather than
		// resolving a wildcard record under an unknown zone.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"unexpose", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a platform block, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "unexpose/requires_platform_config", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stub", func(t *testing.T) {
		// Real-run exercises the live delete-side effect (the only branch
		// --dry-run cannot reach): a stubbed kubectl lets the pdnsutil exec
		// succeed, proving RunUnexposeService's non-dry-run path actually
		// calls deleteDNSRecord and reports success.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"unexpose", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "unexpose/real_run_via_stub", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_skip_if_unconfigured", func(t *testing.T) {
		// --skip-if-unconfigured turns the missing-platform-block failure above
		// into a traced no-op success, for a caller (the delete Job) composing
		// unexpose after delete without knowing whether the target was ever
		// exposed at all.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"unexpose", "team", "dev", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "unexpose/dry_run_skip_if_unconfigured", normalize.Apply(result.Combined))
	})
}
