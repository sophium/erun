package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// TestJobOffEnvironmentExclusiveClaimIsEnforced: an exclusive job started by a
// host caller through a real MCP edge makes a second job start fail, naming
// the holder.
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

	// emcp stands in for the environment's edge; ERUN_REPO_REMOTE=true makes its
	// jobs in-environment work.
	emcpEnv := append(append([]string{}, setup.Env()...), fixture.StubEnv(stubs, "work")...)
	emcpEnv = append(emcpEnv, "ERUN_ERUN_BIN="+bin, "ERUN_REPO_REMOTE=true")

	emcpBin := emcpBinaryPath(t)
	emcpCmd := harnessexec.Command(emcpBin,
		"--host", "127.0.0.1", "--port", fmt.Sprint(mcpPort),
		"--metrics-port", fmt.Sprint(metricsPort),
		"--tenant", "team", "--environment", "dev", "--repo-path", repoPath,
	)
	emcpCmd.Env = emcpEnv
	emcpCmd.Dir = setup.Cwd
	if err := emcpCmd.Start(); err != nil {
		t.Fatalf("start emcp: %v", err)
	}
	// Registered before the job teardowns so LIFO runs it after: they read
	// supervisors back through this edge.
	t.Cleanup(func() {
		_ = emcpCmd.Process.Kill()
		_ = emcpCmd.Wait()
	})
	waitForLocalPort(t, mcpPort)

	// The host caller: no ERUN_REPO_REMOTE.
	hostEnv := setup.Env()

	start := erun.Run(t, []string{"exec", "job", "start", "--tenant", "team", "--environment", "dev", "--name", "gate", "--id", "gate", "--exclusive", "--", "work"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if start.ExitCode != 0 {
		t.Fatalf("start gate: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, []byte("go\n"), 0o644)
		for _, id := range []string{"gate", "intruder"} {
			stopOffEnvironmentJob(t, setup, hostEnv, id)
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
