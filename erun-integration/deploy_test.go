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

	t.Run("dry_run_remote_env_uses_embedded_chart", func(t *testing.T) {
		// Regression: a remote env (Remote=true) has its repopath on the
		// remote host's filesystem (e.g. proxmox1: /home/erun/git/erun) and
		// has no local checkout at all. Deploy from any cwd must still
		// work: the embedded default-devops chart is materialized to a
		// temp dir and used for the helm install. Pre-fix, deploy stat'd
		// the remote repopath locally and failed with
		// "open <remote-path>: no such file or directory".
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		// Note: no SeedDevopsRepo — there is no local checkout anywhere.
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: setup.Env()})
		if strings.Contains(result.Combined, "no such file or directory") {
			t.Fatalf("regression: deploy stat'd remote repopath locally:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "helm chart not found") {
			t.Fatalf("regression: deploy did not fall back to embedded chart for remote env:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/dry_run_remote_env_uses_embedded_chart", normalize.Apply(result.Combined))
	})

	t.Run("default_skips_optin_backend_charts", func(t *testing.T) {
		// Regression for issue #271: when a tenant repo contains the runtime
		// chart and the three opt-in backend charts, `erun deploy` without
		// --components must deploy only the runtime chart. The backend
		// charts ship as separate Helm releases and are gated behind the
		// --components flag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if !strings.Contains(result.Combined, "deploy: resolved 1 spec(s)") {
			t.Fatalf("expected default deploy to resolve only the runtime chart, got:\n%s", result.Combined)
		}
		for _, name := range []string{"erun-backend-postgres", "erun-backend-db", "erun-backend-api"} {
			if strings.Contains(result.Combined, name) {
				t.Fatalf("expected default deploy not to mention opt-in chart %q, got:\n%s", name, result.Combined)
			}
		}
		golden.Equal(t, "deploy/default_skips_optin_backend_charts", normalize.Apply(result.Combined))
	})

	t.Run("components_includes_backend_in_deploy_order", func(t *testing.T) {
		// With --components, the opt-in backend charts must deploy in the
		// fixed dependency order (postgres -> db -> api -> runtime),
		// regardless of the order they appear on the command line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "erun-backend-api,erun-backend-db,erun-backend-postgres",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if !strings.Contains(result.Combined, "deploy: resolved 4 spec(s)") {
			t.Fatalf("expected --components deploy to resolve all four charts, got:\n%s", result.Combined)
		}
		// helm releases must appear in dependency order, not the
		// alphabetical or input order.
		expectedOrder := []string{"erun-backend-postgres", "erun-backend-db", "erun-backend-api", "team-devops"}
		var lastIndex int
		for _, name := range expectedOrder {
			idx := strings.Index(result.Combined[lastIndex:], name)
			if idx < 0 {
				t.Fatalf("expected helm release %q after position %d, got:\n%s", name, lastIndex, result.Combined)
			}
			lastIndex += idx + len(name)
		}
		golden.Equal(t, "deploy/components_includes_backend_in_deploy_order", normalize.Apply(result.Combined))
	})

	t.Run("components_rejects_unknown_name", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
		result := erun.Run(t, []string{
			"deploy", "team", "dev",
			"--version", "1.0.0",
			"--components", "bogus",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown component, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, `unknown deploy component "bogus"`) {
			t.Errorf("expected unknown-component error message, got:\n%s", result.Combined)
		}
		golden.Equal(t, "deploy/components_rejects_unknown_name", normalize.Apply(result.Combined))
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
