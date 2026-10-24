package integration

import (
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

func testOpenVscodeRealRunWithDeployRequiresShellDeployErrors(t *testing.T) {
	t.Parallel()
	// open is pure: it does not deploy. --deploy is the
	// operator-convenience shortcut, but an IDE launch has no shell to
	// host the deploy progress, so even `open --deploy --vscode` must
	// refuse with the actionable "run `erun sshd init` or `erun deploy`
	// first" error instead of deploying behind the IDE's back. (Bare
	// `open --vscode` is pure and never reaches this guard.) The kubectl
	// NotFound stub is the deployment-check decision input; the run fails
	// before any port-forward starts so no ports are needed.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--deploy", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the IDE open needs a runtime deploy, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "open/vscode_real_run_with_deploy_requires_shell_deploy_errors", normalize.Apply(result.Combined))
}

func testOpenVscodeWithoutSshdErrorsWithGuidance(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "open/vscode_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
}

func testOpenIntellijWithoutSshdErrorsWithGuidance(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "open/intellij_without_sshd_errors_with_guidance", normalize.Apply(result.Combined))
}

func testOpenVscodeAndIntellijConflict(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for conflicting flags, got:\n%s", result.Combined)
	}
	golden.Equal(t, "open/vscode_and_intellij_conflict", normalize.Apply(result.Combined))
}

func testOpenVscodeDryRun(t *testing.T) {
	t.Parallel()
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
}

func testOpenIntellijDryRun(t *testing.T) {
	t.Parallel()
	// See vscode_dry_run for the host-OS pinning rationale; IntelliJ
	// has its own platform-conditional code paths (Gateway lookup,
	// installed-app fallback) that diverge by OS, so the golden
	// would otherwise drift between Linux and darwin runners.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
}

func testOpenVscodeRealRunWritesKnownHostsAndLaunchesIde(t *testing.T) {
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
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
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
}

func testOpenIntellijRealRunWritesJetbrainsConfigAndLaunchesIde(t *testing.T) {
	// Same shape as the VSCode real-run, targeting the IntelliJ flow
	// instead. Confirms internal/jetbrainsconfig writers fire (XML
	// configs in the seeded HOME's IntelliJ options dir), the
	// macOS-only `open -a 'IntelliJ IDEA'` bootstrap is invoked, and
	// known_hosts gets populated.
	skipIfPortsBusy(t, 26100, 26122, 26133)
	setup := env.New(t)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	optionsDir := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3", "options")
	envVars := seedIntelliJOpenEnv(t, setup, stubsDir, "AAAAINTELLIJKEY=", optionsDir)
	ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
	fixture.StubBinaryWithScript(t, stubsDir, "open",
		`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
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
	ideArgs, err := os.ReadFile(ideLog)
	if err != nil {
		t.Fatalf("read ide-launcher.log: %v", err)
	}
	if !strings.Contains(string(ideArgs), "IntelliJ IDEA") {
		t.Errorf("expected IDE launcher to invoke 'IntelliJ IDEA', got:\n%s", ideArgs)
	}
}

func testOpenIntellijRealRunLinuxBootstrapsViaPathIdea(t *testing.T) {
	// Linux arm of the IntelliJ flow: the options dir resolves under
	// ~/.config/JetBrains (no darwin Gateway-options upsert), and with
	// no recent Gateway project the installed-app fallback resolves
	// `idea` from PATH via lookPathBootstrapAttempts and launches it
	// detached. ERUN_HOST_OS_OVERRIDE=linux pins the branch
	// (erun-integration/AGENTS.md — platform-dependent goldens).
	skipIfPortsBusy(t, 26100, 26122, 26133)
	setup := env.New(t)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	optionsDir := filepath.Join(setup.Home, ".config", "JetBrains", "IntelliJIdea2024.3", "options")
	envVars := seedIntelliJOpenEnv(t, setup, stubsDir, "AAAALINUXKEY=", optionsDir)
	ideaLog := filepath.Join(setup.Cwd, "idea-launcher.log")
	fixture.StubBinaryWithScript(t, stubsDir, "idea",
		`printf 'idea %s\n' "$*" > '`+ideaLog+`'`+"\n"+`exit 0`+"\n")
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
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
}
