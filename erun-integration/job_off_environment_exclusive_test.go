package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
)

// TestJobOffEnvironmentExclusiveClaimIsEnforced is the exact reproduction
// erun#2080 reported, run against a real MCP edge rather than a dry-run
// trace: an exclusive job started from a host caller (no ERUN_REPO_REMOTE,
// exactly `erun exec job start --exclusive` typed on an operator's machine)
// used to leave a caller in the same off-environment path free to start a
// second job while the first was still running, because --exclusive never
// reached exec_raw's arguments at all -- the edge enforced nothing, and both
// calls reported success. This proves the fix: the second, ordinary job
// start is now refused and names the holder, the same guarantee job_test.go
// already locks for the in-environment path.
func TestJobOffEnvironmentExclusiveClaimIsEnforced(t *testing.T) {
	const mcpPort = 26700
	const metricsPort = 26701
	skipIfPortsBusy(t, mcpPort, metricsPort)

	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpPort)
	fixture.SeedDesktopIdentity(t, setup)
	repoPath := filepath.Join(setup.Home, "git", "team")

	bin := erun.BinaryPath(t)
	stubs := filepath.Join(setup.Cwd, "stubs")
	release := filepath.Join(setup.Cwd, "release")
	fixture.StubBinaryWithScript(t, stubs, "work", "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0\n")

	// emcp stands in for the target environment's own edge, exactly as
	// TestJobOffEnvironmentAgentReinvocation's does; ERUN_REPO_REMOTE=true is
	// what makes the job it starts run as genuinely in-environment work.
	emcpEnv := append(append([]string{}, setup.Env()...), fixture.StubEnv(stubs, "work")...)
	emcpEnv = append(emcpEnv, "ERUN_ERUN_BIN="+bin, "ERUN_REPO_REMOTE=true")

	emcpBin := emcpBinaryPath(t)
	emcpCmd := exec.Command(emcpBin,
		"--host", "127.0.0.1", "--port", fmt.Sprint(mcpPort),
		"--metrics-port", fmt.Sprint(metricsPort),
		"--tenant", "team", "--environment", "dev", "--repo-path", repoPath,
	)
	emcpCmd.Env = emcpEnv
	emcpCmd.Dir = setup.Cwd
	if err := emcpCmd.Start(); err != nil {
		t.Fatalf("start emcp: %v", err)
	}
	t.Cleanup(func() {
		_ = emcpCmd.Process.Kill()
		_ = emcpCmd.Wait()
	})
	waitForLocalPort(t, mcpPort)

	// The host half: a caller outside any environment, exactly the
	// reproduction's `erun exec job start --exclusive`. No ERUN_REPO_REMOTE
	// here -- that is the whole point of this scenario.
	hostEnv := setup.Env()

	start := erun.Run(t, []string{"exec", "job", "start", "--tenant", "team", "--environment", "dev", "--name", "gate", "--id", "gate", "--exclusive", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if start.ExitCode != 0 {
		t.Fatalf("start gate: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, []byte("go\n"), 0o644)
		for _, id := range []string{"gate", "intruder"} {
			erun.Run(t, []string{"exec", "job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
		}
	})

	intruder := erun.Run(t, []string{"exec", "job", "start", "--tenant", "team", "--environment", "dev", "--name", "intruder", "--id", "intruder", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if intruder.ExitCode == 0 {
		t.Fatalf("expected the intruder job start to be refused while gate holds the environment exclusively, got 0:\n%s", intruder.Combined)
	}
	if !strings.Contains(intruder.Combined, "held exclusively") {
		t.Fatalf("expected the refusal to name the exclusive hold, got:\n%s", intruder.Combined)
	}
}
