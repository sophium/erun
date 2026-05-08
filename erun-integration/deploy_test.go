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

func TestDeploy(t *testing.T) {
	t.Run("help_outside_devops_cwd", func(t *testing.T) {
		// Regression for commit a7b4d08: when cwd has no devops context, the
		// deploy command must still be registered so the desktop UI's
		// `erun deploy <tenant> <env> --version X` invocation can land its
		// flags. Pre-fix, this returned the root help and "unknown flag:
		// --version". Lives unskipped so the integration suite fails until
		// erun-cli/cmd/root.go always registers deployCommand().
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stdout, "Deploy the current Helm chart") {
			t.Errorf("expected deploy command help, got root help:\n%s", result.Combined)
		}
		if !strings.Contains(result.Stdout, "--version string") {
			t.Errorf("expected --version flag in deploy help, got:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/help_outside_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("version_flag_recognized_outside_devops_cwd", func(t *testing.T) {
		// A second regression check: even when the flag is set on a real
		// deploy attempt, "unknown flag: --version" must not appear. The
		// command will still fail (no env or no chart) but for a sensible
		// reason rather than flag parsing. Lives unskipped so the suite
		// fails until the deploy registration fix lands.
		setup := env.New(t)
		result := erun.Run(t, []string{"deploy", "missing", "missing", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if strings.Contains(result.Combined, "unknown flag: --version") {
			t.Fatalf("regression: deploy still rejects --version outside devops cwd:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/version_flag_recognized_outside_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_cwd", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_outside_devops_with_tenant_env", func(t *testing.T) {
		// Regression for issue #252: when erun deploy <tenant> <env> is
		// invoked from a cwd outside the devops module (e.g. the desktop
		// UI launching the binary from $HOME for a remote environment),
		// the resolved tenant project root must drive chart discovery
		// instead of cwd. Pre-fix this hit "helm chart not found in
		// current directory" because resolveCurrentDevopsK8sDir gated
		// chart resolution on cwd == projectRoot.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		if strings.Contains(result.Combined, "helm chart not found") {
			t.Fatalf("regression: deploy from outside devops cwd hit helm-chart-not-found:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_outside_devops_with_tenant_env", normalize.Apply(result.Combined))
	})

	t.Run("snapshot_conflict_errors", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--snapshot", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting snapshot flags, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "cannot use --snapshot and --no-snapshot with conflicting values") {
			t.Errorf("expected conflict error message, got:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/snapshot_conflict_errors", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Drive the non-dry-run helm/kubectl runners via stub binaries so
		// the deploy execution path (deploy.go's run* helpers, post-helm
		// kubectl waits, helm-recovery branches) gets covered.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-snapshot"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "deploy/real_run_via_stubs", normalize.Apply(result.Combined))
	})
}
