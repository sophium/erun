package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
)

// TestJobOffEnvironmentAgentReinvocation drives the bounded-reinvocation
// mechanism (job_supervisor.go's considerEnvironmentJobReinvocation) through
// the one dispatch path the existing coverage never exercised: `erun exec job
// start --agent` run from
// OUTSIDE any environment (a host caller), which reaches the target's
// exec_agent MCP tool instead of calling StartEnvironmentJob directly the way
// job_test.go's self-dispatch scenarios do (both run inside the environment,
// via inEnvironment's ERUN_REPO_REMOTE=true).
//
// The two dispatch paths converge on the identical eruncommon.StartEnvironmentJob
// call and the identical `exec job supervise` re-exec, so nothing in the
// session-capture/reinvocation code itself reads how the start request
// arrived -- but that convergence had never been proven end-to-end for the
// off-environment half, only reasoned about. This scenario runs the real
// `emcp` binary (erun-mcp/cmd/emcp) as a genuine subprocess standing in for
// the target environment's own edge, with MCP auth left unconfigured -- the
// documented "legacy loopback-only" mode (erun-mcp/auth.go's
// mcpAuthConfigFromEnv), not a test-only bypass.
//
// emcp runs as a real subprocess rather than in-process (erunmcp.RunHTTP
// called directly from the test goroutine) because erun-common resolves the
// XDG cache/config dirs through adrg/xdg's package-level vars, computed once
// at that package's init and never re-read: a first attempt at this scenario
// ran the server in-process and t.Setenv on XDG_CACHE_HOME never reached it,
// so the in-process StartEnvironmentJob call polled the developer's real
// cache dir for a record the spawned supervisor (a fresh process, which
// resolves xdg fresh) was correctly writing to the isolated sandbox --
// production is unaffected (emcp is always its own fresh process there too),
// but an in-process test double would have been proving the wrong thing.
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

	// The environment side: everything emcp spawns (the detached `exec job
	// supervise` subprocess, the stubbed claude, the nested gate jobs claude
	// starts through its own re-exec of erun) inherits emcp's own explicit
	// env, so it needs the isolated sandbox, the stub routing, the erun
	// binary to supervise with, the gate wait-cap overrides, and
	// ERUN_REPO_REMOTE=true (the *nested* gate job start is genuinely
	// running inside the environment, unlike the outer host call below).
	// Auth left unset is mcpAuthConfigFromEnv's documented loopback-only mode.
	emcpEnv := append(append([]string{}, setup.Env()...), fixture.StubEnv(stubs, "claude", "gatefail", "gateok")...)
	emcpEnv = append(emcpEnv,
		"ERUN_ERUN_BIN="+bin,
		"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=2s",
		"ERUN_JOB_GATE_INCOMPLETE_POLL=20ms",
		"ERUN_REPO_REMOTE=true",
	)

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

	// The host half: a caller outside any environment, exactly `erun exec job
	// start --agent claude` typed on an operator's own machine. No
	// ERUN_REPO_REMOTE here -- that is the whole point of this scenario.
	hostEnv := setup.Env()

	start := erun.Run(t, []string{"exec", "job", "start", "--tenant", "team", "--environment", "dev", "--name", "outer", "--agent", "claude", "--", "fix the failing tests"},
		erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		for _, id := range []string{"gate", "gate-2"} {
			erun.Run(t, []string{"exec", "job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: hostEnv})
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
	emcpBuildErr  error
)

// emcpBinaryPath builds the real erun-mcp/cmd/emcp binary once per test run --
// the same pattern erun.BinaryPath uses for the erun CLI -- so this scenario
// exercises the real MCP transport rather than a hand-rolled stand-in.
func emcpBinaryPath(t *testing.T) string {
	t.Helper()
	emcpBuildOnce.Do(func() {
		moduleDir, err := filepath.Abs(filepath.Join("..", "erun-mcp"))
		if err != nil {
			emcpBuildErr = err
			return
		}
		binDir, err := os.MkdirTemp("", "erun-integration-emcp-bin-")
		if err != nil {
			emcpBuildErr = err
			return
		}
		exe := filepath.Join(binDir, "emcp")
		cmd := exec.Command("go", "build", "-o", exe, "./cmd/emcp")
		cmd.Dir = moduleDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			emcpBuildErr = fmt.Errorf("go build emcp: %w\n%s", buildErr, out)
			return
		}
		emcpBuildPath = exe
	})
	if emcpBuildErr != nil {
		t.Fatalf("build emcp: %v", emcpBuildErr)
	}
	return emcpBuildPath
}

// waitForLocalPort blocks until something accepts a TCP connection on port, so
// the host-side erun.Run calls below never race emcp's own listener startup.
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
