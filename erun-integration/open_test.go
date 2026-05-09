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

// stubKubectlNotFound writes a `kubectl` stub at <stubsDir>/kubectl that mimics
// the response real kubectl returns for a deployment that does not exist:
// non-zero exit code with "Error from server (NotFound)" in the output.
//
// Why a stub matters for --dry-run: open's runtime-deploy short-circuit (in
// CheckKubernetesDeployment via erun-common/deploy.go) reads kubectl output
// to decide whether to redeploy. Without a stub the real kubectl on the
// developer's PATH runs against the test-context that doesn't exist, leaks
// "exit status 1" into the trace, and the chosen branch ends up driven by
// whichever local kubectl happens to be installed. The stub turns the check
// into a deterministic decision input — the dry-run output then reflects the
// open command's branching, not the host's kubectl.
func stubKubectlNotFound(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `Error from server (NotFound): deployments.apps "team-devops" not found`,
		ExitCode: 1,
	})
	return append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
}

func TestOpen(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"open", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/help", normalize.Apply(result.Combined))
	})

	t.Run("no_shell_dry_run", func(t *testing.T) {
		// Default --dry-run: deployment is not yet present, so the runner
		// resolves the runtime helm chart, traces the namespace+helm
		// upgrade, and emits the no-shell setup script for the caller.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(result.Combined, "deploying the devops runtime before opening the shell") {
			t.Fatalf("expected redeploy decision when stub kubectl reports NotFound, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/no_shell_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("snapshot_env_config_drives_local_build", func(t *testing.T) {
		// Regression for the snapshot fallback bug: when the env config
		// has snapshot=true persisted (a prior `erun open --snapshot`),
		// `erun open` without --snapshot must still reach the local-build
		// branch. Pre-fix, allowLocalBuilds was wired to the override
		// only, so the persisted setting was silently ignored and the
		// runtime image always came from EnvConfig.RuntimeVersion.
		setup := env.New(t)
		fixture.SeedTenantEnvWithSnapshot(t, setup, "team", "local", true)
		fixture.SeedDevopsRepo(t, setup, "team", "local")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "local", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(result.Combined, "docker build") {
			t.Fatalf("expected snapshot=true env config to drive local docker build in dry-run, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/snapshot_env_config_drives_local_build", normalize.Apply(result.Combined))
	})

	t.Run("no_snapshot_skips_local_build", func(t *testing.T) {
		// --no-snapshot for a local env where the env config has
		// snapshot=true must force allowLocalBuilds=false: the
		// runtime-image trace must not contain a `docker build` line and
		// the helm chart must use the persisted runtime version.
		setup := env.New(t)
		fixture.SeedTenantEnvWithSnapshot(t, setup, "team", "local", true)
		fixture.SeedDevopsRepo(t, setup, "team", "local")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "local", "--no-shell", "--no-alias-prompt", "--no-snapshot", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if strings.Contains(result.Combined, "docker build") {
			t.Fatalf("expected --no-snapshot to skip local docker build, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/no_snapshot_skips_local_build", normalize.Apply(result.Combined))
	})

	t.Run("version_override_skips_local_build", func(t *testing.T) {
		// --version pins the runtime chart to a specific version; the
		// builder branch must be skipped (no docker build) and the helm
		// upgrade must reference the override version explicitly.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--version", "9.9.9", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if strings.Contains(result.Combined, "docker build") {
			t.Fatalf("expected --version override to skip docker build, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/version_override_skips_local_build", normalize.Apply(result.Combined))
	})

	t.Run("runtime_image_override_uses_default_chart", func(t *testing.T) {
		// --runtime-image rewrites the runtime release to use the
		// embedded default-devops chart with the chosen image. There is
		// no local devops module seeded, so the path exercises
		// applyRuntimeDeployImageOverride's fallback to the embedded
		// chart.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(result.Combined, "ghcr.io/example/custom-runtime") {
			t.Fatalf("expected --runtime-image to surface in helm trace, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/runtime_image_override_uses_default_chart", normalize.Apply(result.Combined))
	})

	t.Run("vscode_without_sshd_errors_with_guidance", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if !strings.Contains(result.Combined, "--vscode requires sshd-enabled") {
			t.Fatalf("expected sshd-required guidance, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/vscode_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
	})

	t.Run("intellij_without_sshd_errors_with_guidance", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if !strings.Contains(result.Combined, "--intellij requires sshd-enabled") {
			t.Fatalf("expected sshd-required guidance, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/intellij_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
	})

	t.Run("vscode_and_intellij_conflict", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting flags, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "--vscode and --intellij cannot be used together") {
			t.Errorf("expected conflict error, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/vscode_and_intellij_conflict", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run_traces_port_forwards", func(t *testing.T) {
		// Exercises cmd/api_port_forward.go and cmd/mcp_port_forward.go:
		// for a remote environment, --dry-run must trace the kubectl
		// port-forward commands that would be started for both API and
		// MCP. With kubectl stubbed to NotFound the runtime helm upgrade
		// and the port-forward traces both appear from the same dry-run
		// output, so the scenario locks both paths in one golden.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(result.Combined, "port-forward") {
			t.Fatalf("expected remote env to emit port-forward trace, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/remote_dry_run_traces_port_forwards", normalize.Apply(result.Combined))
	})

	t.Run("vscode_dry_run", func(t *testing.T) {
		// VSCode against an sshd-enabled remote env: dry-run must reach
		// past validateIDEOptions and emit the redeploy / port-forward /
		// IDE-launch traces. The launchVSCode dependency is a no-op in
		// dry-run (nil launcher) so this scenario stops at the trace
		// boundary without invoking real `code`.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/vscode_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("intellij_dry_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("default_tenant_environment_resolves_from_root_config", func(t *testing.T) {
		// `erun open` without args must pick up defaulttenant +
		// defaultenvironment from $XDG_CONFIG_HOME/erun. Exercises
		// resolveOpenParams' "no args" branch and OpenParamsForArgs.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(result.Combined, "tenant=team environment=dev") {
			t.Fatalf("expected default tenant/env to resolve, got:\n%s", result.Combined)
		}
		golden.Equal(t, "open/default_tenant_environment_resolves_from_root_config", normalize.Apply(result.Combined))
	})
}
