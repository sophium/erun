package integration

import (
	"fmt"
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

func testOpenRealRunMigratesARecordFromTheUserConfigDirLayout(t *testing.T) {
	// Forward records under os.UserConfigDir (not the config home once
	// XDG_CONFIG_HOME is set on macOS) must move into the config tree on open.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	legacyState := seedLegacyPortForwardRecord(t, setup, "mcp", "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
	})...)
	envVars = append(envVars, openHostOSOverride)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/real_run_migrates_a_record_from_the_user_config_dir_layout", normalize.Apply(result.Combined))
	// The canonical log keeps what was written before the move.
	legacyLog := strings.TrimSuffix(legacyState, ".json") + ".log"
	for _, path := range []string{legacyState, legacyLog, legacyLog + ".1"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be moved away, stat err: %v", path, err)
		}
	}
	statePath := portForwardStateFile(setup, "mcp", "team", "dev")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected the record at the config-home layout: %v", err)
	}
	newLog := strings.TrimSuffix(statePath, ".json") + ".log"
	for _, path := range []string{newLog, newLog + ".1"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migrated %s: %v", path, err)
		}
		if !strings.Contains(string(body), legacyPortForwardLogMarker) {
			t.Errorf("expected %s to carry the pre-upgrade log, got:\n%s", path, body)
		}
	}
}

func testOpenRealRunPortForwardStartRecoversFromATransientFailure(t *testing.T) {
	t.Parallel()
	// One forced transient failure per forwarder, below the 3-attempt retry
	// budget, so every forward must still come up.
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
	envVars = append(envVars, "ERUN_PORT_FORWARD_FORCE_TRANSIENT_FAILURES=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "port-forward unavailable") {
		t.Fatalf("a transient failure within the retry budget must not degrade the forward, got:\n%s", result.Combined)
	}
	for _, kind := range []string{"mcp", "sshd"} {
		if _, err := os.Stat(portForwardStateFile(setup, kind, "team", "dev")); err != nil {
			t.Fatalf("expected %s port-forward state to exist after recovering from a transient start failure: %v", kind, err)
		}
	}
}

func testOpenRealRunPortForwardStartDegradesAfterPersistentTransientFailures(t *testing.T) {
	t.Parallel()
	// Past the retry budget open still exits 0 but names the degraded forward.
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
	envVars = append(envVars, "ERUN_PORT_FORWARD_FORCE_TRANSIENT_FAILURES=3")
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, kind := range []string{"MCP", "SSH"} {
		if !strings.Contains(result.Combined, "open: "+kind+" port-forward unavailable; continuing without it") {
			t.Fatalf("expected a %s port-forward unavailable warning, got:\n%s", kind, result.Combined)
		}
	}
	for _, kind := range []string{"mcp", "sshd"} {
		if _, err := os.Stat(portForwardStateFile(setup, kind, "team", "dev")); !os.IsNotExist(err) {
			t.Fatalf("expected %s port-forward state to stay unwritten once the retry budget is exhausted, stat err: %v", kind, err)
		}
	}
}

// Not parallel: binds the fixed 26100 range, and another scenario serving
// those ports would make an unreachable forward look reachable.
func testOpenRealRunPortForwardStartSurvivesADyingListenersSocket(t *testing.T) {
	// A replacement kubectl's bind can be refused while the predecessor's socket
	// is still closing. The stub reproduces that once per port; erun must absorb it.
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
		DeploymentName:         "team-devops",
		ContainerName:          "team-devops",
		RepoPath:               "/home/erun/git/team",
		SSHDEnabled:            true,
		MCPPort:                26100,
		SSHPort:                26122,
		ListenConflictAttempts: 1,
	})...)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "port-forward unavailable") {
		t.Fatalf("a bind refused only while the replaced listener's socket closes must be retried, not reported as an unavailable forward, got:\n%s", result.Combined)
	}
	for _, kind := range []string{"mcp", "sshd", "api"} {
		if _, err := os.Stat(portForwardStateFile(setup, kind, "team", "dev")); err != nil {
			t.Fatalf("expected %s port-forward state to be written once the bind was retried: %v", kind, err)
		}
	}
	// Prove the retried tunnel really serves.
	conn, err := netDialTimeout("tcp", "127.0.0.1:26100", time.Second)
	if err != nil {
		t.Fatalf("MCP port-forward must be listening once the refused bind was retried: %v", err)
	}
	_ = conn.Close()
}

func testOpenRealRunPortForwardStartReportsAPortThatNeverComesFree(t *testing.T) {
	// Not parallel: fixed ports. A port held by a non-socket must fail after a
	// retry window and say the bind was refused, not report a startup timeout.
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
		DeploymentName:         "team-devops",
		ContainerName:          "team-devops",
		RepoPath:               "/home/erun/git/team",
		SSHDEnabled:            true,
		MCPPort:                26100,
		SSHPort:                26122,
		ListenConflictAttempts: 1000,
	})...)
	// --no-shell: failed forwards must fail the run.
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit when every bind is refused, got 0:\n%s", result.Combined)
	}
	for _, port := range []string{"127.0.0.1:26100", "127.0.0.1:26122", "127.0.0.1:26133"} {
		if !strings.Contains(result.Combined, "kubectl could not listen on "+port) {
			t.Fatalf("a refused bind on %s must be reported as itself, got:\n%s", port, result.Combined)
		}
	}
	if strings.Contains(result.Combined, "timed out waiting for") {
		t.Fatalf("a refused bind must not be reported as a startup timeout, got:\n%s", result.Combined)
	}
	// The state file records the attempt, so liveness says the forward is down.
	for _, port := range []int{26100, 26122, 26133} {
		conn, err := netDialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("port %d must not be listening when every bind was refused", port)
		}
	}
}
