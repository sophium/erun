package integration

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// netDialTimeout aliases net.DialTimeout for skipIfErunPortsBusy.
func netDialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

// skipIfPortsBusy short-circuits a real-run open scenario when something on
// the host already listens on one of the scenario's fixed local ports.
// Real-run fixtures persist localportrangestart=26100 (far from erun's
// default 17000 range) precisely so this never fires on a developer machine
// with a live erun session; the guard remains as a last line of defense
// against an unrelated process squatting on the high range.
func skipIfPortsBusy(t *testing.T, ports ...int) {
	t.Helper()
	for _, port := range ports {
		conn, err := netDialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Skipf("port %d is already in use on this host; skipping real-run open scenario", port)
		}
	}
}

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
	stubLsofNoHolder(t, stubs)
	return append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
}

// adoptHolder describes one fake TCP port holder the lsof/ps probe stubs
// present to production's adopt-or-conflict check.
type adoptHolder struct {
	port int
	pid  int
	argv string
}

// stubAdoptHolderProbes overwrites the lsof + ps stubs in the scenario's
// stub dir with versions that present a fake port holder per configured
// port. The lsof stub returns the holder's PID only when the queried
// -iTCP:<port> matches; for every other port it exits 1 (no holder), so
// probes for sibling ports stay silent. The ps stub returns the configured
// argv only for the matching PID.
//
// Returns nothing — the env vars are already wired by the kubectl helper
// because the stubs live in the same directory.
func stubAdoptHolderProbes(t *testing.T, setup env.Setup, holders ...adoptHolder) {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	lsofScript := `for arg in "$@"; do
    case "$arg" in
`
	for _, holder := range holders {
		lsofScript += fmt.Sprintf("        -iTCP:%d) printf '%%s\\n' '%d'; exit 0 ;;\n", holder.port, holder.pid)
	}
	lsofScript += `    esac
done
exit 1
`
	fixture.StubBinaryWithScript(t, stubs, "lsof", lsofScript)
	psScript := `pid=
prev=
for arg in "$@"; do
    if [ "$prev" = "-p" ]; then
        pid="$arg"
    fi
    prev="$arg"
done
case "$pid" in
`
	for _, holder := range holders {
		psScript += fmt.Sprintf("    %d) printf '%%s\\n' %s; exit 0 ;;\n", holder.pid, shellQuote(holder.argv))
	}
	psScript += `esac
exit 1
`
	fixture.StubBinaryWithScript(t, stubs, "ps", psScript)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// waitForFile polls for path until it exists and has content, failing the
// test at the deadline. Used for side effects written by detached
// subprocesses (Start+Release launchers) that may outlive the erun run.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s did not appear with content within %s (last err: %v)", path, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stubKubectlGenericError writes a kubectl stub that exits non-zero with a
// message that does not match the "NotFound" / "no resources found" tokens
// CheckKubernetesDeployment treats as an absent deployment. Used to lock the
// dry-run "kubernetes deployment check failed, assuming not deployed"
// fallback in shouldDeployRuntime (open.go:407-410).
func stubKubectlGenericError(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `Unable to connect to the server: dial tcp 10.0.0.1:443: i/o timeout`,
		ExitCode: 2,
	})
	stubLsofNoHolder(t, stubs)
	return append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
}

// stubLsofNoHolder writes lsof + ps stubs that report no holder for any
// queried port. Integration scenarios that don't intentionally drive the
// adopt-or-conflict probe install these stubs to keep the probe silent and
// the resulting golden host-independent: without them, a developer with a
// leftover `kubectl port-forward` on one of erun's default ports causes
// the probe to fire mid-scenario and corrupt the captured trace.
func stubLsofNoHolder(t *testing.T, stubsDir string) {
	t.Helper()
	fixture.StubBinaryAdvanced(t, stubsDir, "lsof", fixture.StubBinarySpec{
		ExitCode: 1,
	})
	fixture.StubBinaryAdvanced(t, stubsDir, "ps", fixture.StubBinarySpec{
		ExitCode: 1,
	})
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
		golden.Equal(t, "open/no_shell_dry_run", normalize.Apply(result.Combined))
	})

	// zshAliasLine is the POSIX alias maybeConfigureOpenNoShellAlias appends
	// for team/dev when the user accepts the prompt with SHELL=/bin/zsh.
	const zshAliasLine = `alias team-dev='eval "$(erun open team dev --no-shell)"'`

	t.Run("alias_prompt_dry_run_accept_traces_append", func(t *testing.T) {
		// ERUN_FORCE_TTY=1 is the deliberate test seam (mirroring
		// ERUN_HOST_OS_OVERRIDE) that lifts the stdout-TTY gate so the
		// alias-setup flow (maybeConfigureOpenNoShellAlias) runs in the piped
		// harness. SHELL=/bin/zsh pins detectOpenNoShellAliasStartupFile to
		// ~/.zshrc. Accepting the confirm in --dry-run must trace
		// "open: append team-dev alias to ~/.zshrc" and leave the startup
		// file untouched — locks the dry-run-contract fix from b01e323 that
		// gated appendOpenNoShellAlias behind the trace. The alias confirm is
		// the run's single interactive prompt (readline read-ahead would
		// starve a second one).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		zshrc := filepath.Join(setup.Home, ".zshrc")
		if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
			t.Fatalf("seed ~/.zshrc: %v", err)
		}
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
		golden.Equal(t, "open/alias_prompt_dry_run_accept_traces_append", normalize.Apply(result.Combined))
		// Dry-run must not mutate the startup file (side effect outside the
		// captured streams).
		body, err := os.ReadFile(zshrc)
		if err != nil {
			t.Fatalf("read ~/.zshrc: %v", err)
		}
		if string(body) != "# seeded zshrc\n" {
			t.Errorf("dry-run must leave ~/.zshrc untouched, got:\n%s", body)
		}
	})

	t.Run("alias_prompt_decline_prints_hint", func(t *testing.T) {
		// Declining the alias confirm ("n") must fall back to
		// writeOpenNoShellHintLines: the one-liner alias hint on stderr, no
		// file write. ERUN_FORCE_TTY=1 lifts the TTY gate; SHELL=/bin/zsh
		// pins the POSIX dialect and ~/.zshrc startup file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		zshrc := filepath.Join(setup.Home, ".zshrc")
		if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
			t.Fatalf("seed ~/.zshrc: %v", err)
		}
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "n\n"})
		golden.Equal(t, "open/alias_prompt_decline_prints_hint", normalize.Apply(result.Combined))
		body, err := os.ReadFile(zshrc)
		if err != nil {
			t.Fatalf("read ~/.zshrc: %v", err)
		}
		if string(body) != "# seeded zshrc\n" {
			t.Errorf("declined prompt must leave ~/.zshrc untouched, got:\n%s", body)
		}
	})

	t.Run("alias_prompt_accept_appends_alias_real_run", func(t *testing.T) {
		// Real-run arm of the alias setup: accepting the confirm must append
		// the alias line to ~/.zshrc (appendOpenNoShellAlias) and print the
		// "added team-dev to ..." stderr lines locked by the golden. The
		// kubectl stub reports the runtime deployment as deployed-and-matching
		// so no helm deploy runs; the port-forward simulators come up on the
		// pinned 26100 range (same shape as shell_real_run_single_pass).
		// SeedDevopsRepo keeps the runtime spec off the default chart path so
		// the "create team-devops chart?" prompt never fires — the alias
		// confirm stays the subprocess's single prompt (readline read-ahead).
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		zshrc := filepath.Join(setup.Home, ".zshrc")
		if err := os.WriteFile(zshrc, []byte("# seeded zshrc\n"), 0o644); err != nil {
			t.Fatalf("seed ~/.zshrc: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/cwd",
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		envVars = append(envVars, "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/alias_prompt_accept_appends_alias_real_run", normalize.Apply(result.Combined))
		// The append is a side effect outside the captured streams: the
		// seeded content must survive and the alias line must follow it.
		body, err := os.ReadFile(zshrc)
		if err != nil {
			t.Fatalf("read ~/.zshrc: %v", err)
		}
		if want := "# seeded zshrc\n" + zshAliasLine + "\n"; string(body) != want {
			t.Errorf("expected ~/.zshrc to gain the alias line, want:\n%s\ngot:\n%s", want, body)
		}
	})

	t.Run("no_shell_real_run_creates_chart_deploys_and_persists_version", func(t *testing.T) {
		// Real-run open against a local env whose runtime deployment does
		// not exist and whose tenant repo has no devops chart. Covers, in
		// one pass, the branches dry-run cannot reach:
		//   - maybeCreateMissingRuntimeChart's accepted prompt ("y") and
		//     the real EnsureDefaultDevopsChartWithVersion chart write;
		//   - deployRuntime's real helm deploy (helm stub) wrapped in
		//     wrapOpenHelmDeployWithSpinner;
		//   - persistOpenRuntimeVersion's save branch: --version 9.9.9
		//     differs from the persisted 1.0.0, so the env config must be
		//     rewritten with the deployed version (side-effect assert).
		// The kubectl stub reports the deployment NotFound (decision input
		// for shouldDeployRuntime) and runs the port-forward simulator for
		// the post-deploy forwards on the pinned 26100 range. The chart
		// confirm is the run's single prompt; --no-alias-prompt keeps the
		// alias setup out of the way (readline read-ahead). The golden is
		// stdout-only by design: real-run --no-shell silences stderr so an
		// `eval "$(erun open ... --no-shell)"` alias stays quiet
		// (shouldSilenceNoShellOutput).
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName:     "team-devops",
			ContainerName:      "team-devops",
			DeploymentNotFound: true,
		})...)
		fixture.StubBinary(t, stubsDir, "helm", "")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "helm")...)
		result := erun.Run(t, []string{"open", "team", "dev", "--version", "9.9.9", "--no-shell", "--no-alias-prompt"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/no_shell_real_run_creates_chart_deploys_and_persists_version", normalize.Apply(result.Combined))
		// The accepted prompt materialized the default devops chart in the
		// tenant repo (side effect outside the captured streams).
		if _, err := os.Stat(filepath.Join(setup.Cwd, "team-devops", "k8s", "team-devops", "Chart.yaml")); err != nil {
			t.Errorf("expected the default devops chart to be created: %v", err)
		}
		// persistOpenRuntimeVersion stamped the deployed version into the
		// env config.
		envCfg, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if !strings.Contains(string(envCfg), "runtimeversion: 9.9.9") {
			t.Errorf("expected runtimeversion 9.9.9 persisted on the env config, got:\n%s", envCfg)
		}
	})

	t.Run("vscode_real_run_requires_deploy_errors", func(t *testing.T) {
		// Real-run companion to the vscode_dry_run redeploy trace: when the
		// runtime deployment is missing and the user asked for an IDE
		// launch, deployRuntime must refuse with the actionable "run
		// `erun sshd init` or `erun open` first" error instead of deploying
		// behind the IDE's back. The kubectl NotFound stub is the
		// deployment-check decision input; the run fails before any
		// port-forward starts so no ports are needed.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the IDE open needs a runtime deploy, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "open/vscode_real_run_requires_deploy_errors", normalize.Apply(result.Combined))
	})

	t.Run("alias_prompt_skipped_when_alias_configured", func(t *testing.T) {
		// When ~/.zshrc already carries the team-dev alias,
		// detectOpenNoShellAliasStartupFile reports it configured
		// (startupFileHasAlias true branch) and the whole prompt is skipped:
		// no confirm, no hint lines, just the setup script. No stdin is
		// wired, so a leaked prompt would hang and trip the run timeout.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		zshrc := filepath.Join(setup.Home, ".zshrc")
		if err := os.WriteFile(zshrc, []byte(zshAliasLine+"\n"), 0o644); err != nil {
			t.Fatalf("seed ~/.zshrc: %v", err)
		}
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/alias_prompt_skipped_when_alias_configured", normalize.Apply(result.Combined))
	})

	t.Run("alias_prompt_bash_accept_creates_bashrc", func(t *testing.T) {
		// SHELL=/bin/bash exercises openNoShellStartupFiles' bash arm: the
		// candidate list is ~/.bashrc, ~/.bash_profile, ~/.profile, none of
		// which exist, so detectOpenNoShellAliasStartupFile falls through
		// every stat error and offers the preferred ~/.bashrc. Accepting in
		// real-run drives appendOpenNoShellAlias's create-missing-file arm.
		// Real-run shape mirrors alias_prompt_accept_appends_alias_real_run
		// (deployed kubectl stub + port-forward sim on the 26100 range).
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/cwd",
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		envVars = append(envVars, "ERUN_FORCE_TTY=1", "SHELL=/bin/bash")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/alias_prompt_bash_accept_creates_bashrc", normalize.Apply(result.Combined))
		body, err := os.ReadFile(filepath.Join(setup.Home, ".bashrc"))
		if err != nil {
			t.Fatalf("read ~/.bashrc (append must create the missing file): %v", err)
		}
		if want := zshAliasLine + "\n"; string(body) != want {
			t.Errorf("expected fresh ~/.bashrc with the alias line, want:\n%s\ngot:\n%s", want, body)
		}
	})

	t.Run("alias_powershell_dialect_prints_function_hint", func(t *testing.T) {
		// SHELL=/bin/pwsh resolves the PowerShell dialect:
		// detectOpenNoShellAliasStartupFile refuses a startup file, so the
		// flow takes the hint-lines-only arm — "one-liner function:" plus the
		// Invoke-Expression wrapper — and the stdout setup script switches to
		// the PowerShell form (powerShellQuote). No prompt fires, so no
		// stdin is wired.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_FORCE_TTY=1", "SHELL=/bin/pwsh")
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/alias_powershell_dialect_prints_function_hint", normalize.Apply(result.Combined))
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
		envVars = append(envVars, stubDockerNoLocalImages(t, setup)...)
		result := erun.Run(t, []string{"open", "team", "local", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		golden.Equal(t, "open/runtime_image_override_uses_default_chart", normalize.Apply(result.Combined))
	})

	t.Run("vscode_without_sshd_errors_with_guidance", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "open/vscode_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
	})

	t.Run("intellij_without_sshd_errors_with_guidance", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "open/intellij_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
	})

	t.Run("vscode_and_intellij_conflict", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting flags, got:\n%s", result.Combined)
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
		golden.Equal(t, "open/remote_dry_run_traces_port_forwards", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run_propagates_host_credentials_opt_in", func(t *testing.T) {
		// Locks the deploy plumbing that ships host AWS credentials into a
		// remote runtime: when EnvConfig.RemoteHostCredentials is true,
		// the helm command must include
		// `--set cloudContext.useHostCredentials=true` so the chart sets
		// AWS_PROFILE=erun-host on the runtime container. The desktop
		// refresher writes the matching profile into the pod's
		// ~/.aws/credentials at runtime — that path is tested in erun-mcp.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithHostCredentials(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/remote_dry_run_propagates_host_credentials_opt_in", normalize.Apply(result.Combined))
	})

	t.Run("app_session_skip_ensure_dry_run_skips_the_deploy_preflight", func(t *testing.T) {
		// #463: the desktop runs the open/build/deploy preflight once per env
		// (the shared ensure) and spawns every tab with --skip-ensure. The
		// flag must skip the preflight with an explicit trace — and nothing
		// else: the shell preview (the deployment wait + dtach exec) still
		// runs, which is what holds the tab until the ensure's deploy lands.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--skip-ensure", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_skip_ensure_dry_run_skips_the_deploy_preflight", normalize.Apply(result.Combined))
	})

	t.Run("app_session_ai_dry_run_wraps_dtach_and_launches_claude", func(t *testing.T) {
		// #478: the desktop AI tab runs `erun open --app-session ai --ai`. Without
		// --no-shell the dry-run reaches traceShellPreview, so the bootstrap-script
		// block locks that the remote program is wrapped in a persistent dtach
		// session (so reopening reconnects instead of stranding a parallel claude)
		// and that the cwd-guarded claude is that session's create-time program.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_ai_dry_run_wraps_dtach_and_launches_claude", normalize.Apply(result.Combined))
	})

	t.Run("app_session_ai_dry_run_launches_claude_with_model_and_verbose_debug", func(t *testing.T) {
		// #482/#477: when the env config sets a default Claude model that is in
		// the env's available models, and opts in to verbose+debug, the AI
		// session's create-time program must carry `--model <m> --verbose
		// --debug` after the env effort, in both branches of the cwd-guarded
		// resume. The launch-ai.sh block in the golden is the contract the
		// desktop AI tab relies on.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
			"claude:\n"+
				"  models: [opus, fable]\n"+
				"  defaultmodel: fable\n"+
				"  verbosedebug: true\n"+
				"  effort: high\n")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_ai_dry_run_launches_claude_with_model_and_verbose_debug", normalize.Apply(result.Combined))
	})

	t.Run("app_session_ai_dry_run_drops_default_model_not_in_available", func(t *testing.T) {
		// #482: a persisted defaultmodel that is no longer among the env's
		// available models must not reach the launch — the guard falls back to
		// no --model flag rather than launching Claude on a model the env does
		// not expose.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithClaude(t, setup, "team", "dev",
			"claude:\n"+
				"  models: [opus]\n"+
				"  defaultmodel: fable\n")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "ai", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_ai_dry_run_drops_default_model_not_in_available", normalize.Apply(result.Combined))
	})

	t.Run("app_session_shell_dry_run_wraps_dtach", func(t *testing.T) {
		// The ERun and custom "Terminal N" tabs run `erun open --app-session open-N`:
		// the same persistent dtach session but running a plain interactive shell —
		// no claude launch and no contribute prelude in the launcher body.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_shell_dry_run_wraps_dtach", normalize.Apply(result.Combined))
	})

	t.Run("app_session_contribute_ai_dry_run_preludes_clone", func(t *testing.T) {
		// #478: the contribute-AI tab runs `erun open --app-session contribute-ai
		// --contribute --ai`. The persistent dtach launcher must prepend the
		// contribute prelude (contribute toolchain on PATH, ERUN_SKIP_LINT, cd into
		// the cloned repo) before launching the cwd-guarded claude, all inside the
		// reattachable session.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "contribute-ai", "--contribute", "--ai", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/app_session_contribute_ai_dry_run_preludes_clone", normalize.Apply(result.Combined))
	})

	t.Run("vscode_dry_run", func(t *testing.T) {
		// VSCode against an sshd-enabled remote env: dry-run must reach
		// past validateIDEOptions and emit the redeploy / port-forward /
		// IDE-launch traces. The launchVSCode dependency is a no-op in
		// dry-run (nil launcher) so this scenario stops at the trace
		// boundary without invoking real `code`.
		// Pin host OS to darwin via ERUN_HOST_OS_OVERRIDE so the IDE
		// launch command (`open` vs `xdg-open`) is deterministic across
		// developer machines and CI hosts.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/vscode_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("intellij_dry_run", func(t *testing.T) {
		// See vscode_dry_run for the host-OS pinning rationale; IntelliJ
		// has its own platform-conditional code paths (Gateway lookup,
		// installed-app fallback) that diverge by OS, so the golden
		// would otherwise drift between Linux and darwin runners.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
		envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("vscode_real_run_writes_known_hosts_and_launches_ide", func(t *testing.T) {
		// Drives `erun open --vscode` past every dry-run gate by stubbing
		// kubectl as "deployed" with matching env-var JSON, running the
		// port-forward simulator for each port-forward invocation,
		// returning a fake host key from ssh-keyscan, and intercepting
		// the macOS `open` IDE launcher. Asserts:
		//   - internal/sshknownhosts.UpsertDefaultKnownHost wrote a line
		//     to ~/.ssh/known_hosts (real path covered);
		//   - the IDE launcher stub recorded the vscode-remote URI.
		//
		// The env pins localportrangestart=26100 so the simulator ports
		// (26100/26122/26133) never collide with a developer's live erun
		// session on the default 17000 range; the busy-port skip remains
		// only as a last-resort guard.
		skipIfPortsBusy(t, 26100, 26122, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", 26100)
		// Seed a real SSH public key so syncRemoteSSHDKey can resolve it
		// outside dry-run.
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATESTPUB user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			SSHDEnabled:    true,
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 AAAATESTKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "open",
			`printf '%s\n' "$*" > '`+ideLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
		// Pin darwin so the IDE launcher resolves to the stubbed macOS
		// `open` command. On a Linux host production calls xdg-open, which
		// this scenario does not stub. (erun-integration/AGENTS.md —
		// platform-dependent goldens.)
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		knownHostsBody, err := os.ReadFile(filepath.Join(setup.Home, ".ssh", "known_hosts"))
		if err != nil {
			t.Fatalf("read known_hosts: %v", err)
		}
		if !strings.Contains(string(knownHostsBody), "AAAATESTKEY=") {
			t.Errorf("expected ssh-keyscan output in known_hosts, got:\n%s", knownHostsBody)
		}
		ideArgs, err := os.ReadFile(ideLog)
		if err != nil {
			t.Fatalf("read ide-launcher.log: %v", err)
		}
		if !strings.Contains(string(ideArgs), "vscode://vscode-remote/ssh-remote+erun-team-dev") {
			t.Errorf("expected IDE launcher to receive vscode-remote URI, got:\n%s", ideArgs)
		}
	})

	t.Run("intellij_real_run_writes_jetbrains_config_and_launches_ide", func(t *testing.T) {
		// Same shape as the VSCode real-run, targeting the IntelliJ flow
		// instead. Confirms internal/jetbrainsconfig writers fire (XML
		// configs in the seeded HOME's IntelliJ options dir), the
		// macOS-only `open -a 'IntelliJ IDEA'` bootstrap is invoked, and
		// known_hosts gets populated.
		skipIfPortsBusy(t, 26100, 26122, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", 26100)
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATESTPUB user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			SSHDEnabled:    true,
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 AAAAINTELLIJKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		// Pre-create the IntelliJ *options* dir: production globs for
		// JetBrains/IntelliJIdea*/options (open_ide.go::intelliJOptionsCandidates)
		// and silently skips the jetbrainsconfig writers when no candidate
		// matches. Seeding only the version dir (without options/) is the
		// bug that kept internal/jetbrainsconfig at 0% while this scenario
		// stayed green.
		optionsDir := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3", "options")
		if err := os.MkdirAll(optionsDir, 0o755); err != nil {
			t.Fatalf("mkdir IntelliJ options: %v", err)
		}
		ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "open",
			`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
		// Pin darwin: this scenario seeds the macOS JetBrains options dir
		// above and asserts the `open -a 'IntelliJ IDEA'` bootstrap, both
		// macOS-shaped. Without the pin a Linux host resolves a different
		// options dir and launcher and the writers never fire.
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		knownHosts, err := os.ReadFile(filepath.Join(setup.Home, ".ssh", "known_hosts"))
		if err != nil {
			t.Fatalf("read known_hosts: %v", err)
		}
		if !strings.Contains(string(knownHosts), "AAAAINTELLIJKEY=") {
			t.Errorf("expected ssh-keyscan output in known_hosts, got:\n%s", knownHosts)
		}
		// The jetbrainsconfig writers must have persisted the SSH project
		// config into the seeded options dir (side effect outside the
		// captured streams, so asserted directly).
		sshConfigs, err := os.ReadFile(filepath.Join(optionsDir, "sshConfigs.xml"))
		if err != nil {
			t.Fatalf("read sshConfigs.xml (jetbrainsconfig writers did not fire): %v", err)
		}
		if !strings.Contains(string(sshConfigs), "erun-team-dev") {
			t.Errorf("expected sshConfigs.xml to contain the erun-team-dev host alias, got:\n%s", sshConfigs)
		}
		if _, err := os.Stat(filepath.Join(optionsDir, "sshRecentConnections.v2.xml")); err != nil {
			t.Errorf("expected sshRecentConnections.v2.xml to be written: %v", err)
		}
		// The flow invokes `open -a 'IntelliJ IDEA'` after the writes.
		ideArgs, err := os.ReadFile(ideLog)
		if err != nil {
			t.Fatalf("read ide-launcher.log: %v", err)
		}
		if !strings.Contains(string(ideArgs), "IntelliJ IDEA") {
			t.Errorf("expected IDE launcher to invoke 'IntelliJ IDEA', got:\n%s", ideArgs)
		}
	})

	t.Run("intellij_gateway_adopts_forwards_and_launches_gateway", func(t *testing.T) {
		// Covers the JetBrains Gateway launch path end to end plus foreign
		// port-forward adoption, in three passes over one seeded env:
		//   run 1 (real): registerIntelliJProject writes the JetBrains XML
		//     (no latestUsedIde yet, so the flow falls back to
		//     `open -a 'IntelliJ IDEA'`) and starts the port-forward
		//     simulators that the later passes adopt.
		//   patch: the test injects a latestUsedIde block into
		//     sshRecentConnections.v2.xml, exactly as IntelliJ itself would
		//     after a successful remote session, and deletes the per-env
		//     port-forward state files so the next pass cannot recognise the
		//     simulators as its own.
		//   run 2 (dry-run): with lsof/ps probes presenting run 1's live
		//     simulators as foreign kubectl port-forwards, the dry-run
		//     previews adoption ("would adopt"), traces the Gateway config
		//     scaffolding (mkdir/write-xml) WITHOUT creating it, and traces
		//     the java Gateway launch argv. The golden locks that plan.
		//   run 3 (real): adoption fires for MCP+SSHD ("adopted existing
		//     kubectl port-forward" + state files re-written), the Gateway
		//     scaffolding is created for real, and the fake java binary in
		//     the seeded IntelliJ IDEA.app records the
		//     jetbrains-gateway://connect URI.
		// Pinned to darwin: the standalone Gateway launch
		// (intelliJGatewayDarwinLaunchCommand) is macOS-only.
		skipIfPortsBusy(t, 26100, 26122, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", 26100)
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATESTPUB user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			SSHDEnabled:    true,
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 AAAAGATEWAYKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		optionsDir := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3", "options")
		if err := os.MkdirAll(optionsDir, 0o755); err != nil {
			t.Fatalf("mkdir IntelliJ options: %v", err)
		}
		// Fake installed IntelliJ IDEA.app: one lib jar so the Gateway
		// classpath resolves, and a java shim that records its argv.
		contentsDir := filepath.Join(setup.Home, "Applications", "IntelliJ IDEA.app", "Contents")
		if err := os.MkdirAll(filepath.Join(contentsDir, "lib"), 0o755); err != nil {
			t.Fatalf("mkdir IDEA lib: %v", err)
		}
		if err := os.WriteFile(filepath.Join(contentsDir, "lib", "app.jar"), []byte("jar"), 0o644); err != nil {
			t.Fatalf("write IDEA lib jar: %v", err)
		}
		javaLog := filepath.Join(setup.Cwd, "java-launcher.log")
		javaDir := filepath.Join(contentsDir, "jbr", "Contents", "Home", "bin")
		if err := os.MkdirAll(javaDir, 0o755); err != nil {
			t.Fatalf("mkdir IDEA jbr bin: %v", err)
		}
		if err := os.WriteFile(filepath.Join(javaDir, "java"),
			[]byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > '"+javaLog+"'\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write java shim: %v", err)
		}
		ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "open",
			`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=darwin")

		run1 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if run1.ExitCode != 0 {
			t.Fatalf("run 1 exit %d: %s", run1.ExitCode, run1.Combined)
		}

		// Mark the project as previously opened with a concrete IDE, the
		// precondition for the Gateway URI path.
		recentPath := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
		recentBody, err := os.ReadFile(recentPath)
		if err != nil {
			t.Fatalf("read sshRecentConnections.v2.xml: %v", err)
		}
		latestUsedIDE := `<RecentProjectState>
            <option name="latestUsedIde">
              <RecentProjectInstalledIde>
                <option name="buildNumber" value="243.22562.222" />
                <option name="pathToIde" value="` + contentsDir + `" />
                <option name="productCode" value="IU" />
              </RecentProjectInstalledIde>
            </option>`
		patched := strings.Replace(string(recentBody), "<RecentProjectState>", latestUsedIDE, 1)
		if patched == string(recentBody) {
			t.Fatalf("RecentProjectState not found in:\n%s", recentBody)
		}
		if err := os.WriteFile(recentPath, []byte(patched), 0o600); err != nil {
			t.Fatalf("patch sshRecentConnections.v2.xml: %v", err)
		}
		// Drop the state files so the simulators read as foreign forwards.
		for _, kind := range []string{"mcp", "sshd"} {
			if err := os.Remove(filepath.Join(setup.ConfigHome, "erun", "portforward", kind, "team", "dev.json")); err != nil {
				t.Fatalf("remove %s port-forward state: %v", kind, err)
			}
		}
		stubAdoptHolderProbes(t, setup,
			adoptHolder{port: 26100, pid: 4242,
				argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26100:26100 --address 127.0.0.1"},
			adoptHolder{port: 26122, pid: 4243,
				argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 26122:26122 --address 127.0.0.1"},
		)
		envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)

		run2 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if run2.ExitCode != 0 {
			t.Fatalf("run 2 exit %d: %s", run2.ExitCode, run2.Combined)
		}
		golden.Equal(t, "open/intellij_gateway_dry_run_previews_adoption_and_launch", normalize.Apply(run2.Combined))
		// The dry-run must not have created the Gateway launch scaffolding
		// (regression guard for the write-before-gate bug). The config dir
		// itself already exists — run 1's registerIntelliJProject writes
		// JetBrains options there for real — so assert on the two artifacts
		// only the launch path creates: config/info.xml and system/.
		gatewayCaches := filepath.Join(setup.Home, "Library", "Caches", "JetBrains", "IntelliJIdea2024.3", "tmp", "JetBrainsGateway")
		if _, err := os.Stat(filepath.Join(gatewayCaches, "config", "info.xml")); !os.IsNotExist(err) {
			t.Errorf("expected dry-run to leave Gateway info.xml unwritten, stat err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(gatewayCaches, "system")); !os.IsNotExist(err) {
			t.Errorf("expected dry-run to leave Gateway system dir uncreated, stat err: %v", err)
		}

		run3 := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if run3.ExitCode != 0 {
			t.Fatalf("run 3 exit %d: %s", run3.ExitCode, run3.Combined)
		}
		golden.Equal(t, "open/intellij_gateway_real_run_adopts_and_launches", normalize.Apply(run3.Combined))
		// Adoption rewrote both state files claiming the holder PIDs.
		for kind, wantPID := range map[string]int{"mcp": 4242, "sshd": 4243} {
			stateBody, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "portforward", kind, "team", "dev.json"))
			if err != nil {
				t.Fatalf("read adopted %s state: %v", kind, err)
			}
			if !strings.Contains(string(stateBody), fmt.Sprintf(`"processId":%d`, wantPID)) {
				t.Errorf("expected adopted %s state to claim PID %d, got:\n%s", kind, wantPID, stateBody)
			}
		}
		// The Gateway launch went through the java shim with the connect URI.
		javaArgs := waitForFile(t, javaLog, 5*time.Second)
		if !strings.Contains(javaArgs, "jetbrains-gateway://connect#") {
			t.Errorf("expected java shim to receive a jetbrains-gateway connect URI, got:\n%s", javaArgs)
		}
		if _, err := os.Stat(filepath.Join(gatewayCaches, "config", "info.xml")); err != nil {
			t.Errorf("expected real run to create the Gateway config scaffolding: %v", err)
		}
	})

	t.Run("intellij_real_run_linux_bootstraps_via_path_idea", func(t *testing.T) {
		// Linux arm of the IntelliJ flow: the options dir resolves under
		// ~/.config/JetBrains (no darwin Gateway-options upsert), and with
		// no recent Gateway project the installed-app fallback resolves
		// `idea` from PATH via lookPathBootstrapAttempts and launches it
		// detached. ERUN_HOST_OS_OVERRIDE=linux pins the branch
		// (erun-integration/AGENTS.md — platform-dependent goldens).
		skipIfPortsBusy(t, 26100, 26122, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", 26100)
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATESTPUB user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			SSHDEnabled:    true,
			MCPPort:        26100,
			SSHPort:        26122,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 AAAALINUXKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		optionsDir := filepath.Join(setup.Home, ".config", "JetBrains", "IntelliJIdea2024.3", "options")
		if err := os.MkdirAll(optionsDir, 0o755); err != nil {
			t.Fatalf("mkdir IntelliJ options: %v", err)
		}
		ideaLog := filepath.Join(setup.Cwd, "idea-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "idea",
			`printf 'idea %s\n' "$*" > '`+ideaLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=linux")
		result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/intellij_real_run_linux_bootstraps_via_path_idea", normalize.Apply(result.Combined))
		sshConfigs, err := os.ReadFile(filepath.Join(optionsDir, "sshConfigs.xml"))
		if err != nil {
			t.Fatalf("read sshConfigs.xml (jetbrainsconfig writers did not fire): %v", err)
		}
		if !strings.Contains(string(sshConfigs), "erun-team-dev") {
			t.Errorf("expected sshConfigs.xml to contain the erun-team-dev host alias, got:\n%s", sshConfigs)
		}
		// The idea bootstrap is launched detached (Start+Release); wait for
		// the stub to record the invocation.
		if got := waitForFile(t, ideaLog, 5*time.Second); !strings.HasPrefix(got, "idea") {
			t.Errorf("expected the PATH idea stub to record its launch, got:\n%s", got)
		}
	})

	t.Run("default_tenant_environment_resolves_from_root_config", func(t *testing.T) {
		// `erun open` without args must pick up defaulttenant +
		// defaultenvironment from $XDG_CONFIG_HOME/erun. Exercises
		// resolveOpenParams' "no args" branch and OpenParamsForArgs.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/default_tenant_environment_resolves_from_root_config", normalize.Apply(result.Combined))
	})

	t.Run("kubectl_error_assumes_not_deployed", func(t *testing.T) {
		// kubectl stub exits non-zero with a non-NotFound error. In
		// dry-run, shouldDeployRuntime traces "assuming not deployed" and
		// proceeds with the helm upgrade. Locks the dry-run fallback in
		// shouldDeployRuntime (open.go:407-410).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlGenericError(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/kubectl_error_assumes_not_deployed", normalize.Apply(result.Combined))
	})

	t.Run("not_initialized_triggers_init_retry", func(t *testing.T) {
		// `erun open team dev` with no config triggers
		// resolveOpenWithInitStopForParams' init-fired branch:
		// resolveOpen errors with ErrNotInitialized, shouldRunInitForOpenCommand
		// matches, runInitBeforeOpenForParams runs init in dry-run, and the
		// command exits via initRan=true (open.go:226-230). The init dry-run
		// trace is captured in the golden alongside open's audit line.
		setup := env.New(t)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/not_initialized_triggers_init_retry", normalize.Apply(result.Combined))
	})

	t.Run("tenant_cloud_provider_issuers_flow_into_runtime_deploy", func(t *testing.T) {
		// Exercises ResolveTenantCloudProviderIssuers +
		// CloudProviderOIDCIssuerURL: when the tenant config names cloud
		// provider aliases and the root config carries their OIDC issuer
		// URLs, the resolved runtime helm plan must pass the deduplicated
		// issuer list via api.oidcAllowedIssuers (visible in the dry-run
		// helm trace). Two aliases share one issuer to lock the dedup
		// branch. The tenant devops chart is seeded because only the
		// tenant-chart spec path runs configureDeployInputMetadata — the
		// materialized default-chart path skips it and would leave the
		// issuer list empty.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		root := filepath.Join(setup.ConfigHome, "erun")
		mustWrite(t, filepath.Join(root, "config.yaml"),
			"defaulttenant: team\n"+
				"cloudproviders:\n"+
				"  - alias: alice+123456789012@aws\n"+
				"    provider: aws\n"+
				"    username: alice\n"+
				"    accountid: \"123456789012\"\n"+
				"    oidcissuerurl: https://oidc.eu-west-2.amazonaws.com/team-issuer\n"+
				"  - alias: bob+123456789012@aws\n"+
				"    provider: aws\n"+
				"    username: bob\n"+
				"    accountid: \"123456789012\"\n"+
				"    oidcissuerurl: https://oidc.eu-west-2.amazonaws.com/team-issuer\n",
		)
		mustWrite(t, filepath.Join(root, "team", "config.yaml"),
			"projectroot: "+setup.Cwd+"\n"+
				"name: team\n"+
				"defaultenvironment: dev\n"+
				"cloudprovideraliases:\n"+
				"  - alice+123456789012@aws\n"+
				"  - bob+123456789012@aws\n",
		)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/tenant_cloud_provider_issuers_flow_into_runtime_deploy", normalize.Apply(result.Combined))
	})

	t.Run("default_env_not_configured_runs_init_with_tenant", func(t *testing.T) {
		// `erun open` (no args) against a tenant whose config lacks
		// defaultenvironment: resolveOpen fails with
		// ErrDefaultEnvironmentNotConfigured, the init-retry path resolves
		// init params via initParamsForOpenDefaults — loadOpenDefaultTenant
		// succeeds, loadOpenDefaultEnvironment reports not-configured — and
		// init runs in dry-run with the tenant pre-filled. Locks the
		// tenant-only fallback arm that the fully-seeded scenarios skip.
		// The handed-off init asks to initialize the default environment;
		// declining ("n") ends the run after that single prompt (readline
		// read-ahead allows no second prompt) with init's cancellation
		// message.
		setup := env.New(t)
		root := filepath.Join(setup.ConfigHome, "erun")
		if err := os.MkdirAll(filepath.Join(root, "team"), 0o755); err != nil {
			t.Fatalf("mkdir tenant config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("defaulttenant: team\n"), 0o644); err != nil {
			t.Fatalf("write root config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "team", "config.yaml"),
			[]byte("projectroot: "+setup.Cwd+"\nname: team\n"), 0o644); err != nil {
			t.Fatalf("write tenant config: %v", err)
		}
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "n\n"})
		golden.Equal(t, "open/default_env_not_configured_runs_init_with_tenant", normalize.Apply(result.Combined))
	})

	t.Run("tenant_flag_only_resolves_default_env", func(t *testing.T) {
		// `erun open --tenant team` (flag, no positional args) lands in
		// resolveOpenParams' "tenant set, environment empty" switch case
		// (open.go:172-174), with UseDefaultEnvironment=true so the env
		// resolves from the tenant config. OpenParamsForArgs treats a
		// single positional arg as the *environment*, not the tenant, so
		// the tenant-only branch is only reachable via the --tenant flag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "--tenant", "team", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/tenant_flag_only_resolves_default_env", normalize.Apply(result.Combined))
	})

	t.Run("environment_positional_resolves_default_tenant", func(t *testing.T) {
		// `erun open dev` (single positional arg) lands in
		// resolveOpenParams' "tenant empty, environment set" switch case
		// (open.go:169-171), with UseDefaultTenant=true so the tenant
		// resolves from the root config's defaulttenant. Locks the second
		// switch case which existing 0-arg and 2-arg scenarios skip.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/environment_positional_resolves_default_tenant", normalize.Apply(result.Combined))
	})

	t.Run("remote_runtime_image_override", func(t *testing.T) {
		// Remote env + --runtime-image rewrites the runtime release to
		// use the embedded default-devops chart with the chosen image.
		// Locks the RemoteRepo() branch in applyRuntimeDeployImageOverride
		// (open.go:602-603), which the local-env runtime_image scenario
		// does not reach.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--runtime-image", "ghcr.io/example/custom-runtime", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/remote_runtime_image_override", normalize.Apply(result.Combined))
	})

	t.Run("persisted_local_port_range_is_honoured", func(t *testing.T) {
		// EnvConfig.LocalPortRangeStart is the durable per-env port
		// contract. When it is already set on disk, open must derive every
		// service port from it (MCP 17500, API 17533, SSH 17522) and must
		// not emit the "config: would assign" trace. Locks the early-return
		// in EnsureLocalPortRangePersisted (open.go::common helper).
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17500)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/persisted_local_port_range_is_honoured", normalize.Apply(result.Combined))
	})

	t.Run("walker_skips_index_claimed_by_other_tenant", func(t *testing.T) {
		// When a second env on the host has already persisted
		// localportrangestart=17000, the alphabetical walker must skip
		// that index when allocating an unpersisted env. team/dev sorts
		// before other/staging but the persisted claim wins, so team/dev
		// resolves to 17100 and the dry-run trace records the would-be
		// assignment. Locks the cross-tenant claim-respect branch in
		// ResolveAllEnvironmentLocalPorts.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/walker_skips_index_claimed_by_other_tenant", normalize.Apply(result.Combined))
	})

	t.Run("overlapping_persisted_ranges_fail_with_pointer", func(t *testing.T) {
		// Two envs that persist the same localportrangestart must surface
		// an ErrLocalPortRangeOverlap pointing at both. The CLI should
		// fail with a non-zero exit and a message naming both envs and the
		// range, instead of silently re-allocating one of them. Locks the
		// overlap path in ResolveAllEnvironmentLocalPorts.
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17000)
		fixture.SeedSecondaryTenantEnv(t, setup, "other", "staging", 17000)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/overlapping_persisted_ranges_fail_with_pointer", normalize.Apply(result.Combined))
	})

	t.Run("misaligned_persisted_range_fails_with_pointer", func(t *testing.T) {
		// localportrangestart must align to EnvironmentPortRangeSize=100
		// boundaries from LowerServicePort=17000. A value like 17050
		// would make MCP/API/SSH offsets bleed into another env's range,
		// so the resolver refuses with a clear alignment error instead of
		// accepting it. Locks environmentPortIndexForRangeStart's
		// validation branch.
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 17050)
		envVars := stubKubectlNotFound(t, setup)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/misaligned_persisted_range_fails_with_pointer", normalize.Apply(result.Combined))
	})

	t.Run("adopts_existing_kubectl_port_forward", func(t *testing.T) {
		// Regression for the orphan-kubectl-can't-adopt failure: when the
		// per-env state file is missing but a long-lived `kubectl
		// port-forward` is already serving the env's MCP port, erun must
		// recognise the holder as its own and reuse it instead of erroring
		// with "already in use". The probe runs in dry-run too, so we lock
		// the decision in the golden via the "would adopt" trace.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		stubAdoptHolderProbes(t, setup, adoptHolder{port: 17000, pid: 99999,
			argv: "kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 17000:17000 --address 127.0.0.1"})
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/adopts_existing_kubectl_port_forward", normalize.Apply(result.Combined))
	})

	t.Run("refuses_to_bind_when_foreign_process_holds_port", func(t *testing.T) {
		// When the port is held by a process whose argv does not look like
		// the kubectl port-forward erun would start, adoption is unsafe.
		// erun must trace what is holding the port (PID + argv) so the
		// user sees what to kill, instead of the bare "already in use"
		// message that gave nothing actionable in the prior UI loop.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := stubKubectlNotFound(t, setup)
		stubAdoptHolderProbes(t, setup, adoptHolder{port: 17000, pid: 88888,
			argv: "/usr/local/bin/some-foreign-process --bind 127.0.0.1:17000"})
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/refuses_to_bind_when_foreign_process_holds_port", normalize.Apply(result.Combined))
	})

	t.Run("deployment_match_ignores_missing_api_port", func(t *testing.T) {
		// Regression for the --intellij short-circuit on tenant-owned
		// devops charts. The runtime-pod identity matcher must accept a
		// deployment whose containers expose ERUN_REPO_PATH,
		// ERUN_SSHD_ENABLED, ERUN_MCP_PORT, and ERUN_SSHD_PORT but not
		// ERUN_API_PORT — the erun-api service is a separate deployment
		// with its own port-forward path, so the runtime pod legitimately
		// omits that env var. Without the fix, every open against a
		// tenant chart predating ERUN_API_PORT would be flagged as "not
		// deployed" and either redeploy unnecessarily (shell flow) or
		// hard-error (--intellij / --vscode flow).
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			MCPPort:        17000,
			SSHPort:        17022,
		})...)
		// Keep the adopt-or-conflict probe silent: this scenario is about
		// the deployment-match path, not orphan adoption, and we don't
		// want the developer's host state to leak a "would refuse" line
		// into the golden when port 17000 is in use locally.
		stubLsofNoHolder(t, stubsDir)
		envVars = append(envVars, fixture.StubEnv(stubsDir, "lsof", "ps")...)
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/deployment_match_ignores_missing_api_port", normalize.Apply(result.Combined))
	})

	t.Run("shell_real_run_single_pass_via_kubectl_stub", func(t *testing.T) {
		// Real-run (no --dry-run) shell flow, single pass: the kubectl stub
		// reports the runtime deployment as deployed-and-matching, the
		// port-forward simulators come up on the pinned 26100 range, and the
		// interactive `exec -it` bootstrap exits 0, so runShellLoop's happy
		// path ends after one iteration with exit 0. The env is deliberately
		// LOCAL (remote: false) with a real git repo + ~/.ssh seedables:
		// that is the only shape that drives ExecShell's seedRemoteSSHKey
		// end to end (resolveGitRemote → parseSSHConfig →
		// loadPrivateKeyMaterial → `kubectl exec -i` with the key on stdin)
		// plus loadKnownHostsLines for the bootstrap's known_hosts seeding.
		// The exec stub exit code is the decision input runShellLoop branches
		// on; side-effect asserts cover what the captured streams cannot:
		// the key bytes that flowed to the stub's stdin (never argv) and the
		// RecordEnvironmentActivity cli.json marker.
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
		// A real tenant devops chart keeps the runtime spec off the default
		// chart path, so real-run open does not stop at the interactive
		// "create team-devops chart?" prompt (AGENTS.md: prefer fixtures
		// that bypass prompts over scripted stdin).
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", "git@github.com:acme/widgets.git")
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.ssh: %v", err)
		}
		// ssh config with a comment, a blank line, and a non-matching Host
		// entry so parseSSHConfig's skip/flush branches run, plus the
		// matching github.com entry whose ~-prefixed IdentityFile exercises
		// expandSSHPath.
		if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(
			"# integration test ssh config\n"+
				"\n"+
				"Host *.example.org\n"+
				"  IdentityFile ~/.ssh/other_key\n"+
				"Host github.com\n"+
				"  IdentityFile ~/.ssh/test_ed25519\n"), 0o600); err != nil {
			t.Fatalf("write ssh config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "test_ed25519"), []byte(
			"-----BEGIN OPENSSH PRIVATE KEY-----\nFAKEKEYMATERIAL\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
			t.Fatalf("write private key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(
			"github.com ssh-ed25519 AAAAGITHUBHOSTKEY=\n"+
				"unrelated.example.net ssh-rsa AAAAOTHERKEY=\n"), 0o600); err != nil {
			t.Fatalf("write known_hosts: %v", err)
		}
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		seededKeyFile := filepath.Join(setup.Cwd, "seeded-key")
		// env.New always names the cwd leaf "cwd", so the remote worktree
		// the deployment must advertise is the deterministic
		// /home/erun/git/cwd (RemoteShellWorktreePath of the local repo).
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/cwd",
			MCPPort:        26100,
			SSHPort:        26122,
			ExecExitCodes:  []int{0},
			SeedKeyFile:    seededKeyFile,
		})...)
		result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/shell_real_run_single_pass_via_kubectl_stub", normalize.Apply(result.Combined))
		// The private key must have reached the pod on the seeding exec's
		// stdin (side effect outside the captured streams).
		seeded, err := os.ReadFile(seededKeyFile)
		if err != nil {
			t.Fatalf("read seeded key (seedRemoteSSHKey did not stream the key): %v", err)
		}
		if !strings.Contains(string(seeded), "FAKEKEYMATERIAL") {
			t.Errorf("expected the private key material on the seed exec's stdin, got:\n%s", seeded)
		}
		// The interactive shell exec ran exactly once.
		execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
		if got := strings.Count(execCalls, "call"); got != 1 {
			t.Errorf("expected 1 interactive exec call, got %d", got)
		}
		// recordActivity persisted the CLI activity marker for the env.
		if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "cli.json")); err != nil {
			t.Errorf("expected RecordEnvironmentActivity to write cli.json: %v", err)
		}
	})

	t.Run("shell_real_run_session_taken_over_exits_with_notice", func(t *testing.T) {
		// #478 takeover handover, end to end: the persistent session's exec
		// wrapper exits 76 when another ERun window re-attaches the session.
		// ExecShell must map that to ErrShellSessionTakenOver and
		// runShellLoop must end cleanly — exit 0, no relaunch — after
		// printing the stable ShellSessionTakenOverNotice line the desktop
		// matches to stop its reconnect loop. The exec stub's exit code 76
		// is the decision input that drives the branch. Replaces the deleted
		// unit test cmd.TestRunShellLoopEndsCleanlyWhenSessionTakenOver.
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			MCPPort:        26100,
			SSHPort:        26122,
			ExecExitCodes:  []int{76},
		})...)
		result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("takeover must end the shell loop cleanly, exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/shell_real_run_session_taken_over_exits_with_notice", normalize.Apply(result.Combined))
	})

	t.Run("shell_real_run_failed_rollout_prints_pod_diagnostics", func(t *testing.T) {
		// When the deployment-availability wait fails in real-run, the open
		// flow must not stop at kubectl's bare "exit status 1": ExecShell's
		// enrichShellDeploymentError loads `get pods -o json` + `get events
		// -o json` and renders the runtime pod diagnostics so the user sees
		// why the rollout is stuck. The canned pods JSON drives the
		// formatter's branches (waiting/terminated/running container states,
		// init containers, conditions with reason+message, pod
		// status reason/message, >3 pods omitted); the canned events JSON
		// drives the warning filter, per-pod matching via involvedObject and
		// regarding, every timestamp-field fallback, count/series/deprecated
		// counts, and the >5 events omission line. Exit code 1 is cobra's
		// RunE error exit — asserted exactly so a silent success can never
		// pass.
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		podsJSON := `{
  "items": [
    {
      "metadata": {"name": "team-devops-aaa"},
      "status": {
        "phase": "Pending",
        "conditions": [
          {"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/3 nodes are available: 3 Insufficient memory."}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-bbb"},
      "status": {
        "phase": "Running",
        "conditions": [
          {"type": "Ready", "status": "False", "reason": "ContainersNotReady", "message": "containers with unready status: [team-devops]"},
          {"type": "ContainersReady", "status": "False"}
        ],
        "initContainerStatuses": [
          {"name": "init-workspace", "ready": true, "restartCount": 0, "state": {"terminated": {"exitCode": 0, "reason": "Completed", "startedAt": "2026-06-01T09:58:00Z", "finishedAt": "2026-06-01T09:58:05Z"}}}
        ],
        "containerStatuses": [
          {"name": "team-devops", "ready": false, "restartCount": 4, "state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 2m40s restarting failed container=team-devops"}}, "lastState": {"terminated": {"exitCode": 1, "reason": "Error", "startedAt": "2026-06-01T09:59:00Z", "finishedAt": "2026-06-01T09:59:10Z"}}}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-ccc"},
      "status": {
        "phase": "Running",
        "reason": "SandboxChanged",
        "message": "Pod sandbox changed, it will be killed and re-created.",
        "containerStatuses": [
          {"name": "team-devops", "ready": true, "restartCount": 1, "state": {"running": {"startedAt": "2026-06-01T10:01:00Z"}}}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-ddd"},
      "status": {"phase": "Pending"}
    }
  ]
}`
		eventsJSON := `{
  "items": [
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedScheduling", "message": "0/3 nodes are available: 3 Insufficient memory.", "count": 3, "lastTimestamp": "2026-06-01T10:06:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "BackOff", "note": "Back-off pulling image registry.example/test/team-devops", "eventTime": "2026-06-01T10:05:00Z", "series": {"count": 7, "lastObservedTime": "2026-06-01T10:05:30Z"}},
    {"regarding": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedMount", "message": "MountVolume.SetUp failed for volume workspace", "deprecatedCount": 2, "deprecatedLastTimestamp": "2026-06-01T10:04:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "Unhealthy", "message": "Readiness probe failed: connection refused", "metadata": {"creationTimestamp": "2026-06-01T10:03:00Z"}},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedCreatePodSandBox", "message": "Failed to create pod sandbox", "firstTimestamp": "2026-06-01T10:02:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "Evicted", "message": "Low memory"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Normal", "reason": "Pulled", "message": "Container image already present"},
    {"involvedObject": {"name": "some-other-pod"}, "type": "Warning", "reason": "Unrelated", "message": "must be filtered out"}
  ]
}`
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			MCPPort:        26100,
			SSHPort:        26122,
			WaitExitCode:   1,
			WaitStderr:     "error: timed out waiting for the condition on deployments/team-devops",
			PodsJSON:       podsJSON,
			EventsJSON:     eventsJSON,
		})...)
		result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 1 {
			t.Fatalf("expected exit 1 from the failed rollout wait, got %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/shell_real_run_failed_rollout_prints_pod_diagnostics", normalize.Apply(result.Combined))
	})

	t.Run("shell_real_run_reattach_deploy_cycles_once", func(t *testing.T) {
		// The in-shell `erun deploy` handoff: the bootstrap exits 75 when the
		// user requests a deploy from inside the remote shell. runShellLoop
		// must map that to ErrShellReattachDeploy, run the managed deploy
		// (remote env → embedded chart, helm/docker stubbed to succeed), and
		// re-enter the loop; the second exec exits 0 and the run ends 0. The
		// stateful exec stub (first call 75, then 0, tracked in the
		// exec-calls file) is the decision input; the file's two lines prove
		// the loop really re-entered kubectl exec after the deploy.
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			MCPPort:        26100,
			SSHPort:        26122,
			ExecExitCodes:  []int{75, 0},
		})...)
		fixture.StubBinary(t, stubsDir, "helm", "")
		fixture.StubBinary(t, stubsDir, "docker", "")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "helm", "docker")...)
		result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/shell_real_run_reattach_deploy_cycles_once", normalize.Apply(result.Combined))
		execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
		if got := strings.Count(execCalls, "call"); got != 2 {
			t.Errorf("expected 2 interactive exec calls (handoff + reattach), got %d:\n%s", got, execCalls)
		}
	})

	t.Run("shell_real_run_pod_replaced_reattaches", func(t *testing.T) {
		// kubectl exec dying with 137 (SIGKILL — the runtime pod was
		// replaced under the shell) must not surface as an error when the
		// replacement pod is already healthy: ExecShell probes `get pods`,
		// runtimePodLooksLikeCleanReplacement accepts the Running/Ready/
		// restartCount=0 pod, and runShellLoop silently reattaches
		// (continue). The second exec exits 0 → overall exit 0. The silence
		// is the contract — the golden locks that no error or notice line
		// appears — so the exec-calls file is the proof that a second exec
		// actually happened.
		skipIfPortsBusy(t, 26100, 26133)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
		stubsDir := filepath.Join(setup.Cwd, "stubs")
		healthyPodsJSON := `{
  "items": [
    {
      "metadata": {"name": "team-devops-fresh"},
      "status": {
        "phase": "Running",
        "conditions": [
          {"type": "Ready", "status": "True"},
          {"type": "ContainersReady", "status": "True"}
        ],
        "containerStatuses": [
          {"name": "team-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-06-01T10:10:00Z"}}}
        ]
      }
    }
  ]
}`
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
			DeploymentName: "team-devops",
			ContainerName:  "team-devops",
			RepoPath:       "/home/erun/git/team",
			MCPPort:        26100,
			SSHPort:        26122,
			ExecExitCodes:  []int{137, 0},
			PodsJSON:       healthyPodsJSON,
		})...)
		result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("pod replacement must reattach cleanly, exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "open/shell_real_run_pod_replaced_reattaches", normalize.Apply(result.Combined))
		execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
		if got := strings.Count(execCalls, "call"); got != 2 {
			t.Errorf("expected 2 interactive exec calls (replaced + reattach), got %d:\n%s", got, execCalls)
		}
	})

}
