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
	// An earlier release kept forward records beside os.UserConfigDir,
	// which on macOS is not the config home once XDG_CONFIG_HOME is set.
	// The first real open after the upgrade must move that record, its log
	// and the log's rotated generation into the config tree rather than
	// leave them stranded where nothing reads them again.
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
	// Filesystem state outside the captured streams: nothing is left at
	// the legacy layout, and the canonical log still carries what was
	// written before the upgrade, which a fresh log would not.
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
	// A real erun open once exited 0 while the mcp port-forward state
	// file was never written -- a transient OS resource squeeze (fd or
	// process-table exhaustion under heavy parallel load) failed the
	// forward's own start (opening its log file / forking kubectl)
	// silently, since open treats a forwarder failure as best-effort and
	// degrades rather than failing the whole command. This scenario
	// forces exactly one synthetic transient failure per forwarder via
	// the ERUN_PORT_FORWARD_FORCE_TRANSIENT_FAILURES test seam
	// (port_forward_start_retry.go) -- fewer than the 3-attempt retry
	// budget -- so both the mcp and sshd forwards must still end up
	// established.
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
	// The other half of the contract above: a transient failure that
	// outlasts the retry budget must still leave open exiting 0
	// (best-effort forwarders never fail the whole command), but must
	// name the degraded forward in its own trace rather than staying
	// silent about the state file it never wrote.
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

// Not parallel: these two bind the fixture's fixed 26100 range for real,
// and a sibling scenario serving those ports would make an unreachable
// forward look reachable (canReachLocalMCPEndpoint answers off whatever
// holds the port). A sequential subtest never overlaps a paused parallel
// one, so it is isolated from the parallel real-run scenarios above.
func testOpenRealRunPortForwardStartSurvivesADyingListenersSocket(t *testing.T) {
	// The reported failure, end to end and for real: a reattach starts its
	// replacement kubectl moments after stopping the predecessor, and the
	// predecessor's listening socket outlives the process that held it, so
	// the replacement's bind is refused and kubectl exits. Measured at
	// roughly 58% of reattachments, each one costing a whole retry window
	// during which the environment is unreachable.
	//
	// The race cannot be staged on demand — it is a kernel socket teardown
	// completing a few milliseconds after a kill — so the kubectl stub is
	// made to produce exactly its observable shape, deterministically, for
	// the first bind of every local port: kubectl's own two "Unable to
	// listen" lines on stderr and exit 1, then a real simulator on the next
	// attempt. All three forwarders are driven through it at once, since
	// they share this code path, and the assertion is that erun absorbs it:
	// every forward comes up, and open never reports one unavailable.
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
	// The MCP simulator is the one the retry brought up: prove the tunnel is
	// really serving, not merely recorded.
	conn, err := netDialTimeout("tcp", "127.0.0.1:26100", time.Second)
	if err != nil {
		t.Fatalf("MCP port-forward must be listening once the refused bind was retried: %v", err)
	}
	_ = conn.Close()
}

func testOpenRealRunPortForwardStartReportsAPortThatNeverComesFree(t *testing.T) {
	// Not parallel: fixed ports, and it asserts the forwards are down.
	// See the note on the scenario above.
	// The bound on the retry above, and the diagnosis it replaces: a port
	// held by something that is not a socket on its way closed must still
	// fail — after a retry window, not forever — and must say what actually
	// happened. Before this, a refused bind was indistinguishable from a pod
	// that never answered: the wait spent its whole startup timeout and
	// reported "timed out waiting for MCP port-forward", which sends the
	// operator looking for a pod problem they do not have.
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
	// --no-shell because the forwards are the whole request: a run whose
	// forwards never came up must fail rather than read as success.
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
	// Nothing came up: the state file records the attempt that was made
	// (the launch itself succeeded), so liveness — not the record — is what
	// says the forward is down.
	for _, port := range []int{26100, 26122, 26133} {
		conn, err := netDialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("port %d must not be listening when every bind was refused", port)
		}
	}
}
