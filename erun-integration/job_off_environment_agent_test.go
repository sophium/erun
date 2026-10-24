package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// TestJobOffEnvironmentAgentReinvocation drives bounded reinvocation through a
// host `erun exec job start --agent` reaching a real emcp edge (auth unset,
// loopback-only mode). emcp runs as a subprocess because adrg/xdg resolves XDG
// dirs once at init, so an in-process server would ignore the sandbox.
func TestJobOffEnvironmentAgentReinvocation(t *testing.T) {
	const mcpPort = 26500
	const metricsPort = 26501
	skipIfPortsBusy(t, mcpPort, metricsPort)

	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithSSHDPortRange(t, setup, "team", "dev", mcpPort)
	fixture.SeedDesktopIdentity(t, setup)
	repoPath := filepath.Join(setup.Home, "git", "team")

	bin := erun.BinaryPath(t)
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "gatefail", "sleep 0.1\nexit 1\n")
	fixture.StubBinaryWithScript(t, stubs, "gateok", "sleep 0.1\nexit 0\n")
	claudeScript := fmt.Sprintf(`case "$*" in
  *--resume*)
    printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
    %q exec job start --tenant team --environment dev --name gate --id gate-2 -- gateok >/dev/null 2>&1
    printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"fixed the gate"}\n'
    ;;
  *)
    printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
    %q exec job start --tenant team --environment dev --name gate --id gate -- gatefail >/dev/null 2>&1
    printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"started the gate"}\n'
    ;;
esac
`, bin, bin)
	fixture.StubBinaryWithScript(t, stubs, "claude", claudeScript)

	// Everything emcp spawns inherits this env. ERUN_REPO_REMOTE=true because the
	// nested gate jobs run in-environment; PrivateCoverDir because emcp is not
	// started through erun.Run.
	emcpEnv := append(append([]string{}, setup.Env()...), fixture.StubEnv(stubs, "claude", "gatefail", "gateok")...)
	emcpEnv = append(emcpEnv,
		"ERUN_ERUN_BIN="+bin,
		"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=2s",
		"ERUN_JOB_GATE_INCOMPLETE_POLL=20ms",
		"ERUN_REPO_REMOTE=true",
		erun.CoverDirEnv+"="+erun.PrivateCoverDir(t),
	)

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
	// Registered before the job teardown so LIFO runs it after: that teardown
	// reads supervisors back through this edge.
	t.Cleanup(func() {
		_ = emcpCmd.Process.Kill()
		_ = emcpCmd.Wait()
	})
	waitForLocalPort(t, mcpPort)

	// The host caller: no ERUN_REPO_REMOTE.
	hostEnv := setup.Env()

	start := erun.Run(t, []string{"exec", "job", "start", "--tenant", "team", "--environment", "dev", "--name", "outer", "--agent", "claude", "--", "fix the failing tests"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		for _, id := range []string{"gate", "gate-2"} {
			stopOffEnvironmentJob(t, setup, hostEnv, id)
		}
	})

	await := erun.Run(t, []string{"exec", "job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if await.ExitCode != 0 {
		t.Fatalf("expected outer to eventually succeed once its bounded resumed turn fixed the gate, got %d:\n%s", await.ExitCode, await.Combined)
	}

	statusJSON := erun.Run(t, []string{"exec", "job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	var payload struct {
		State             string `json:"state"`
		Succeeded         bool   `json:"succeeded"`
		StartedJobFailed  string `json:"startedJobFailed"`
		ReinvocationCount int    `json:"reinvocationCount"`
	}
	if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
		t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
	}
	if payload.State != "exited" || !payload.Succeeded || payload.StartedJobFailed != "" || payload.ReinvocationCount != 1 {
		t.Fatalf("expected outer to report exited/succeeded with exactly one resumed turn, got %+v", payload)
	}
}

var (
	emcpBuildOnce sync.Once
	emcpBuildPath string
	errEmcpBuild  error
)

// emcpBinaryPath builds erun-mcp/cmd/emcp once per test run.
func emcpBinaryPath(t *testing.T) string {
	t.Helper()
	emcpBuildOnce.Do(func() {
		moduleDir, err := filepath.Abs(filepath.Join("..", "erun-mcp"))
		if err != nil {
			errEmcpBuild = err
			return
		}
		binDir, err := os.MkdirTemp("", "erun-integration-emcp-bin-")
		if err != nil {
			errEmcpBuild = err
			return
		}
		exe := filepath.Join(binDir, "emcp")
		cmd := harnessexec.Command("go", "build", "-o", exe, "./cmd/emcp")
		cmd.Dir = moduleDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			errEmcpBuild = fmt.Errorf("go build emcp: %w\n%s", buildErr, out)
			return
		}
		emcpBuildPath = exe
	})
	if errEmcpBuild != nil {
		t.Fatalf("build emcp: %v", errEmcpBuild)
	}
	return emcpBuildPath
}

// waitForLocalPort blocks until something accepts on port.
func waitForLocalPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing accepted a connection on %s within the deadline", addr)
}
