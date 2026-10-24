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
	// An IDE launch has no shell to host deploy progress, so even --deploy refuses.
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
	// ERUN_HOST_OS_OVERRIDE pins the launch command (`open` vs `xdg-open`).
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/vscode_dry_run", normalize.Apply(result.Combined))
}

func testOpenIntellijDryRun(t *testing.T) {
	t.Parallel()
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHD(t, setup, "team", "dev")
	envVars := append(stubKubectlNotFound(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"open", "team", "dev", "--intellij", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "open/intellij_dry_run", normalize.Apply(result.Combined))
}

func testOpenVscodeRealRunWritesKnownHostsAndLaunchesIde(t *testing.T) {
	// Ports pinned at 26100 to avoid a live session on the default 17000 range.
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
	fixture.StubBinary(t, stubsDir, "ssh-keyscan", "[127.0.0.1]:26122 ssh-ed25519 AAAATESTKEY=")
	envVars = append(envVars, fixture.StubEnv(stubsDir, "ssh-keyscan")...)
	ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
	fixture.StubBinaryWithScript(t, stubsDir, "open",
		`printf '%s\n' "$*" > '`+ideLog+`'`+"\n"+`exit 0`+"\n")
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
	// On Linux production calls xdg-open, which is not stubbed.
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
	skipIfPortsBusy(t, 26100, 26122, 26133)
	setup := env.New(t)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	optionsDir := filepath.Join(setup.Home, "Library", "Application Support", "JetBrains", "IntelliJIdea2024.3", "options")
	envVars := seedIntelliJOpenEnv(t, setup, stubsDir, "AAAAINTELLIJKEY=", optionsDir)
	ideLog := filepath.Join(setup.Cwd, "ide-launcher.log")
	fixture.StubBinaryWithScript(t, stubsDir, "open",
		`printf '%s\n' "$*" >> '`+ideLog+`'`+"\n"+`exit 0`+"\n")
	envVars = append(envVars, "PATH="+stubsDir+string(os.PathListSeparator)+setup.PathDir)
	// The seeded options dir and launcher are macOS-shaped.
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
	// ERUN_HOST_OS_OVERRIDE=linux pins the branch.
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
	// The bootstrap is launched detached; wait for the stub to record it.
	if got := waitForFile(t, ideaLog, 5*time.Second); !strings.HasPrefix(got, "idea") {
		t.Errorf("expected the PATH idea stub to record its launch, got:\n%s", got)
	}
}
