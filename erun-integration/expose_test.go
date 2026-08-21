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
		// Happy path: a platform block plus an env yields a complete expose plan with no side effects.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_tls", func(t *testing.T) {
		// --no-tls takes the http branch: the plan traces "http-only" and the
		// rendered Ingress carries no tls block, only ingressClassName.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--no-tls", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run_no_tls", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_cross_cluster", func(t *testing.T) {
		// The platform env that owns PowerDNS sits on a different cluster than the
		// target env: the wildcard DNS write must exec against the platform env's
		// kube context while the Ingress applies against the target env's, and the
		// two must never collapse into one cross-cluster misroute.
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
		// block it fails with an actionable error rather than resolving a hostname
		// under an unknown zone.
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
		// Without platform.env the per-env wildcard DNS write would exec as
		// `kubectl -n "" exec` and silently misroute, so expose fails fast.
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

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Drive the live expose path (the block RunExposeService reaches only after
		// the dry-run short-circuit: RequireKubernetesContext, the pdnsutil
		// replace-rrset exec, and the Host-routing Ingress apply) via a kubectl stub
		// so the real-run execution branch gets covered, not just the dry-run trace.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/real_run_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("skip_if_unconfigured_no_platform", func(t *testing.T) {
		// --skip-if-unconfigured turns the missing-platform-block hard failure
		// (see requires_platform_config above) into a traced no-op success, the
		// behavior a caller composing expose after another command needs when it
		// cannot know in advance whether the target project is a platform
		// deployment.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_no_platform", normalize.Apply(result.Combined))
	})

	t.Run("skip_if_unconfigured_with_platform", func(t *testing.T) {
		// --skip-if-unconfigured must not change behavior for an actual platform
		// deployment: with a platform block present, it resolves and traces the
		// full plan exactly like the plain dry_run scenario above.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_with_platform", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_platform_override_no_project", func(t *testing.T) {
		// --services-zone/--platform-namespace supply what a project checkout
		// would otherwise resolve, so expose runs from a directory with no git
		// repo at all -- the shape a hosted deploy Job runs in, which has no
		// checkout to read .erun/config.yaml from (#1086).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10",
			"--services-zone", "services.erunpaas.com", "--platform-namespace", "frs-prod", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/dry_run_platform_override_no_project", normalize.Apply(result.Combined))
	})

	t.Run("platform_override_requires_both", func(t *testing.T) {
		// Half the override configured is the same as neither: expose refuses
		// rather than resolving a plan from an incomplete pair.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10",
			"--services-zone", "services.erunpaas.com", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with only --services-zone set, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/platform_override_requires_both", normalize.Apply(result.Combined))
	})

	t.Run("skip_if_unconfigured_no_project", func(t *testing.T) {
		// --skip-if-unconfigured must cover "no project at all", not just "a
		// project with no platform block" -- the hole #1086 reported: the deploy
		// Job's --skip-if-unconfigured could not save it because project
		// resolution itself failed outright with "cannot find git project"
		// before the skip decision ever ran.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_no_project", normalize.Apply(result.Combined))
	})

	t.Run("requires_project_without_override_or_skip", func(t *testing.T) {
		// Interactive `erun expose` from a plain, non-git directory still fails
		// fast -- the override flags and --skip-if-unconfigured are opt-in, not
		// a silent relaxation of the default project requirement.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_project_without_override_or_skip", normalize.Apply(result.Combined))
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
