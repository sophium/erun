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

// skipIfErunPortsBusy short-circuits a real-run open scenario when the
// developer's host is already running erun on its default port range
// (17000/17022/17033). The integration suite has no way to convince
// production code to use a different port range without changing the
// public default, so tests that exercise the real port-forward path skip
// instead of clobbering the dev session.
func skipIfErunPortsBusy(t *testing.T) {
	t.Helper()
	for _, port := range []int{17000, 17022, 17033} {
		conn, err := netDialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Skipf("port %d is already in use on this host (likely a running erun); skipping real-run open scenario", port)
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

// stubAdoptHolderProbes overwrites the lsof + ps stubs that
// stubKubectlNotFound installed with versions that present a fake port
// holder for one specific TCP port. The lsof stub returns holderPID only
// when the queried -iTCP:<port> matches holderPort; for every other port
// it exits 1 (no holder), so the API/SSHD probes for sibling ports stay
// silent. The ps stub returns the configured argv only for holderPID.
//
// Returns nothing — the env vars are already wired by the kubectl helper
// because the two stubs live in the same directory.
func stubAdoptHolderProbes(t *testing.T, setup env.Setup, holderPID, holderPort int, holderArgv string) {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	lsofScript := fmt.Sprintf(`for arg in "$@"; do
    case "$arg" in
        -iTCP:%d) printf '%%s\n' '%d'; exit 0 ;;
    esac
done
exit 1
`, holderPort, holderPID)
	fixture.StubBinaryWithScript(t, stubs, "lsof", lsofScript)
	psScript := fmt.Sprintf(`pid=
prev=
for arg in "$@"; do
    if [ "$prev" = "-p" ]; then
        pid="$arg"
    fi
    prev="$arg"
done
if [ "$pid" = "%d" ]; then
    printf '%%s\n' %s
    exit 0
fi
exit 1
`, holderPID, shellQuote(holderArgv))
	fixture.StubBinaryWithScript(t, stubs, "ps", psScript)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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
		// erun's port allocation deterministically picks 17000/17022/17033
		// for the first seeded tenant/env. On developer machines running
		// a real erun runtime, those ports are already taken; skip rather
		// than fight the collision.
		skipIfErunPortsBusy(t)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
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
			MCPPort:        17000,
			SSHPort:        17022,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:17022 ssh-ed25519 AAAATESTKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "open",
			`printf '%s\n' "$*" > '`+ideLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
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
		skipIfErunPortsBusy(t)
		setup := env.New(t)
		fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
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
			MCPPort:        17000,
			SSHPort:        17022,
		})...)
		fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:17022 ssh-ed25519 AAAAINTELLIJKEY=")
		envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
		// Pre-create the IntelliJ JetBrains options dir so the writers
		// have a place to land. The flow probes for IntelliJ to be
		// installed; if no candidate dir exists the bootstrap branch
		// short-circuits and we miss the writer coverage.
		jetbrainsRoot := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3")
		if err := os.MkdirAll(jetbrainsRoot, 0o755); err != nil {
			t.Fatalf("mkdir IntelliJ options: %v", err)
		}
		ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
		fixture.StubBinaryWithScript(t, stubsDir, "open",
			`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
		envVars = append(envVars, "PATH="+stubsDir+":"+os.Getenv("PATH"))
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
		// JetBrains writers persist the SSH project config. The flow
		// will have invoked `open -a 'IntelliJ IDEA'` after the writes.
		ideArgs, err := os.ReadFile(ideLog)
		if err != nil {
			t.Fatalf("read ide-launcher.log: %v", err)
		}
		if !strings.Contains(string(ideArgs), "IntelliJ IDEA") {
			t.Errorf("expected IDE launcher to invoke 'IntelliJ IDEA', got:\n%s", ideArgs)
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
		stubAdoptHolderProbes(t, setup, 99999, 17000,
			"kubectl --context test-context --namespace team-dev port-forward deployment/team-devops 17000:17000 --address 127.0.0.1")
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
		stubAdoptHolderProbes(t, setup, 88888, 17000,
			"/usr/local/bin/some-foreign-process --bind 127.0.0.1:17000")
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
		result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "open/deployment_match_ignores_missing_api_port", normalize.Apply(result.Combined))

}
