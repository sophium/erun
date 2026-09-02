package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// The job scenarios drive the real detach-and-observe path rather than a
// dry-run trace: the whole contract is what a supervisor running in the
// environment observes about work it waited on, which no plan can show. The
// work itself is a declared stub binary, so what a job reports is fixed by the
// scenario and not by whatever the host happens to have installed.

// jobStubEnv declares the stub the job scenarios run as their work and returns
// the scenario environment routed at it. The stub is reached through the
// ERUN_<NAME>_BIN seam every erun-spawned binary honors, so the scrubbed PATH
// stays scrubbed.
func jobStubEnv(t *testing.T, setup env.Setup, script string) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "work", script)
	return inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "work")...))
}

// jobStubSignal is the line a job stub writes to say it reached a point in its
// run, so a scenario synchronizes on an observable condition rather than on a
// wall-clock sleep that a loaded machine would make flaky. It carries content
// because waitForFile waits for a non-empty file.
const jobStubSignal = "reached\\n"

func jobRecordPath(setup env.Setup, tenant, environment, id string) string {
	return filepath.Join(setup.CacheHome, "erun", "activity", tenant, environment, "jobs", id+".json")
}

// startJob starts a job and registers a cleanup that kills it. The whole point
// of a job is that it outlives the call that started it, so a scenario that
// fails before releasing its work would otherwise strand a process past the test
// run; cancelling an already-finished job is a successful no-op, so the cleanup
// is unconditional.
func startJob(t *testing.T, setup env.Setup, envVars []string, name string, args ...string) erun.Result {
	t.Helper()
	start := append([]string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", name}, args...)
	result := erun.Run(t, start, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	t.Cleanup(func() {
		erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", name, "--signal", "KILL"},
			erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	})
	return result
}

// waitForJobActivity blocks until the supervisor has folded enough of an agent's
// event stream to name an activity. The fold runs on the supervisor's own poll,
// so a scenario that read straight after the stub flushed would race it; waiting
// on the observable condition is what keeps the read deterministic instead of
// timing-dependent.
func waitForJobActivity(t *testing.T, setup env.Setup, envVars []string, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", id, "--output", "json"},
			erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			Progress struct {
				Activity string `json:"activity"`
			} `json:"progress"`
		}
		if json.Unmarshal([]byte(result.Stdout), &payload) == nil && payload.Progress.Activity != "" {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("job %q reported no agent activity within %s:\n%s", id, timeout, result.Combined)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestJob(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/help", normalize.Apply(result.Combined))
	})

	t.Run("start_refuses_a_dir_that_does_not_exist_by_name", func(t *testing.T) {
		// An unusable working directory used to reach exec, where Go reports it
		// as an ENOENT naming the *binary* — so a dir the supervisor could not
		// resolve surfaced as a missing shell and sent the reader after a broken
		// image instead of a bad path.
		setup := env.New(t)
		envVars := jobStubEnv(t, setup, "printf '"+jobStubSignal+"'")
		result := startJob(t, setup, envVars, "baddir", "--dir", "no-such-subdir", "--", "work")
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal for a missing dir, got exit 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "no-such-subdir") {
			t.Fatalf("the refusal must name the dir, got:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "fork/exec") {
			t.Fatalf("a bad dir must not surface as an exec failure, got:\n%s", result.Combined)
		}
	})

	t.Run("start_runs_the_work_in_a_relative_dir", func(t *testing.T) {
		// The supervisor is detached with no inherited working directory, so a
		// relative dir has to be anchored before it is handed over, the same way
		// every other repo-facing surface reads a path.
		setup := env.New(t)
		workdir := filepath.Join(setup.Cwd, "workdir-sub")
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			t.Fatalf("create the work directory: %v", err)
		}
		envVars := jobStubEnv(t, setup, "pwd > pwd.txt")
		result := startJob(t, setup, envVars, "reldir", "--dir", "workdir-sub", "--", "work")
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		got := strings.TrimSpace(waitForFile(t, filepath.Join(workdir, "pwd.txt"), 30*time.Second))
		if filepath.Base(got) != "workdir-sub" {
			t.Fatalf("work ran in %q, want it resolved to %s", got, workdir)
		}
	})

	t.Run("await_help", func(t *testing.T) {
		// The await exit codes are the contract an orchestrator branches on, so the
		// help that states them is locked here.
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "await", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/await_help", normalize.Apply(result.Combined))
	})

	t.Run("status_help", func(t *testing.T) {
		// The job states -- including abandoned/gate-incomplete, and that an
		// agent job's own process exiting is not the verdict -- are the
		// contract an orchestrator reads this help for, so it is locked here.
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "status", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/status_help", normalize.Apply(result.Combined))
	})

	t.Run("start_dry_run_plans_the_detach", func(t *testing.T) {
		// The plan must show the supervisor argv, where output will land, and that
		// a lease is taken, without starting anything.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "exit 0")
		result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--", "work", "--all"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/start_dry_run_plans_the_detach", normalize.Apply(result.Combined))
		if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "suite")); !os.IsNotExist(err) {
			t.Errorf("a dry-run start must not register a job, stat err: %v", err)
		}
	})

	t.Run("start_env_dry_run_plans_the_supervisor_argv", func(t *testing.T) {
		// --env must show up in the supervisor argv the plan traces, sorted
		// so the plan is deterministic regardless of flag order.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "exit 0")
		result := erun.Run(t, []string{
			"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run",
			"--env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000", "--env", "SOME_FLAG=1",
			"--", "work",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/start_env_dry_run_plans_the_supervisor_argv", normalize.Apply(result.Combined))
	})

	t.Run("start_handoff_dry_run_plans_the_supervisor_argv", func(t *testing.T) {
		// --handoff must reach the supervisor argv, the same way --env does above,
		// so the deliberate-handoff record actually lands: without --handoff on
		// the supervisor's own invocation, the started job would default into its
		// parent's finish check like any other.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "exit 0")
		result := erun.Run(t, []string{
			"job", "start", "--tenant", "team", "--environment", "dev", "--name", "release", "--dry-run", "--handoff",
			"--", "work",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/start_handoff_dry_run_plans_the_supervisor_argv", normalize.Apply(result.Combined))
	})

	t.Run("start_env_refuses_a_name_that_could_redirect_the_job", func(t *testing.T) {
		// PATH/LD_PRELOAD/etc. are refused up front: letting a caller
		// override them would let it redirect what the job's own process executes
		// or loads, which is a hijack rather than a tuning knob.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "exit 0")
		result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--dry-run", "--env", "PATH=/tmp/evil", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal for --env PATH=..., got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, `"PATH"`) {
			t.Fatalf("the refusal must name the rejected var, got:\n%s", result.Combined)
		}
		if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "suite")); !os.IsNotExist(err) {
			t.Errorf("a refused start must not register a job, stat err: %v", err)
		}
	})

	t.Run("start_env_reaches_the_jobs_own_process", func(t *testing.T) {
		// The real point of --env: the value is not just traced, it is set
		// on the job's actual process, on top of what it inherits.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, `printf '%s' "$SOME_JOB_VAR" > seen.txt`)
		result := startJob(t, setup, envVars, "envcheck", "--env", "SOME_JOB_VAR=hello-job", "--", "work")
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		got := waitForFile(t, filepath.Join(setup.Cwd, "seen.txt"), 30*time.Second)
		if got != "hello-job" {
			t.Fatalf("job process saw SOME_JOB_VAR=%q, want %q", got, "hello-job")
		}
	})

	t.Run("start_help", func(t *testing.T) {
		// The agent switch and what it does to the invocation are only discoverable
		// here, so the help that states them is locked.
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/start_help", normalize.Apply(result.Combined))
	})

	t.Run("agent_start_dry_run_plans_the_streaming_invocation", func(t *testing.T) {
		// An agent job names a tool and a prompt; erun builds the argv. The plan is
		// where that argv is auditable, and the streaming flags are the whole point:
		// without them the tool prints nothing until it exits, so a multi-hour run
		// would report no output while it is actively editing files.
		//
		// Both tools are stubbed present: the dry-run plan also resolves whether the
		// named tool is available in this environment, which is a real
		// filesystem/PATH check that runs regardless of --dry-run, the same way the
		// job dir check does.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinary(t, stubs, "claude", "")
		fixture.StubBinary(t, stubs, "codex", "")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude", "codex")...))
		claude := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude", "--dry-run", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if claude.ExitCode != 0 {
			t.Fatalf("exit %d: %s", claude.ExitCode, claude.Combined)
		}
		codex := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "codex", "--dry-run", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if codex.ExitCode != 0 {
			t.Fatalf("exit %d: %s", codex.ExitCode, codex.Combined)
		}
		golden.Equal(t, "job/agent_start_dry_run_plans_the_streaming_invocation", normalize.Apply(claude.Combined+codex.Combined))
	})

	t.Run("agent_start_refuses_when_the_tool_is_not_available", func(t *testing.T) {
		// A knowable-before-spend precondition: an agent job naming a tool this
		// environment does not have refuses before it takes a lease or starts a
		// supervisor, rather than accepting the job and failing a minute later on
		// the tool's own (often credential-shaped) error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "claude", "--", "fix the failing tests"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal when claude is not installed in this environment, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, `agent tool "claude" is not available in this environment`) {
			t.Fatalf("the refusal must name the tool and say it is unavailable, got:\n%s", result.Combined)
		}
		if _, err := os.Stat(jobRecordPath(setup, "team", "dev", "sweep")); !os.IsNotExist(err) {
			t.Errorf("a refused start must not register a job, stat err: %v", err)
		}
	})

	t.Run("agent_progress_reports_an_authentication_failure_as_the_reason", func(t *testing.T) {
		// The tool is present but its credentials are stale — a real failure the
		// preflight above cannot catch (the tool is installed). The job's own
		// folded progress already carries the tool's raw auth message; the failed
		// job's reason must say plainly that this looks like a credential problem
		// in the environment, not a bug in the work, rather than leaving the raw
		// vendor string to speak for itself.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude",
			`printf '{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1,"result":"Failed to authenticate: OAuth session expired and could not be refreshed"}\n'`+"\n"+
				"exit 1")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := startJob(t, setup, envVars, "sweep", "--agent", "claude", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		var payload struct {
			Reason   string `json:"reason"`
			Progress struct {
				Error string `json:"error"`
			} `json:"progress"`
		}
		if err := json.Unmarshal([]byte(status.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, status.Stdout)
		}
		if !strings.Contains(payload.Progress.Error, "OAuth session expired") {
			t.Fatalf("expected the raw vendor message preserved in progress.error, got %q", payload.Progress.Error)
		}
		if !strings.Contains(payload.Reason, "credentials are missing or stale") {
			t.Fatalf("expected the reason to reframe the failure as a credential problem, got %q", payload.Reason)
		}
	})

	t.Run("agent_start_refuses_an_unsupported_tool_or_a_command", func(t *testing.T) {
		// A caller that meant a command and a caller that meant an agent must not be
		// silently given the other, and an unsupported tool must name the ones erun
		// can actually stream.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		unsupported := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "sweep", "--agent", "gemini", "--dry-run", "--", "do the thing"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if unsupported.ExitCode == 0 {
			t.Fatalf("expected an unsupported agent tool to fail, got 0:\n%s", unsupported.Combined)
		}
		golden.Equal(t, "job/agent_start_refuses_an_unsupported_tool_or_a_command", normalize.Apply(unsupported.Combined))
	})

	t.Run("agent_progress_is_readable_while_the_agent_works", func(t *testing.T) {
		// The defect this kind closes: a detached agent run reporting nothing but
		// "running" for its whole life. The stub emits the tool's real stream-json
		// event shapes and then blocks, so the scenario reads mid-run on an
		// observable condition and locks that erun answers what the agent is doing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		flushed := filepath.Join(setup.Cwd, "flushed")
		release := filepath.Join(setup.Cwd, "release")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude",
			`printf '{"type":"system","subtype":"init","session_id":"s1"}\n'`+"\n"+
				`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Starting the sweep."}]}}\n'`+"\n"+
				`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"erun-common/mcp_client.go"}}]}}\n'`+"\n"+
				"printf '"+jobStubSignal+"' > '"+flushed+"'\n"+
				"while [ ! -f '"+release+"' ]; do sleep 0.05; done\n"+
				`printf '{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}\n'`+"\n"+
				`printf '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Fixed the client."}\n'`+"\n"+
				"exit 0")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := startJob(t, setup, envVars, "sweep", "--agent", "claude", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		waitForFile(t, flushed, 30*time.Second)
		waitForJobActivity(t, setup, envVars, "sweep", 30*time.Second)

		// job_output already serves the events, and job status answers the activity
		// — the two together are what replace scraping the agent's own transcript.
		midRun := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		// The lease already names the work for the desktop; an agent job renames it
		// to the current activity so "busy" becomes "editing <file>".
		lease := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})

		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		finished := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/agent_progress_is_readable_while_the_agent_works", normalize.Apply(
			midRun.Combined+status.Combined+lease.Combined+finished.Combined))
	})

	t.Run("agent_progress_is_the_same_shape_for_every_tool", func(t *testing.T) {
		// Normalizing inside erun is what keeps the progress contract stable across
		// AI tools. Codex reports work as thread items rather than tool calls, and
		// the caller still reads one activity line and one pair of counts.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "codex",
			`printf '{"type":"thread.started","thread_id":"th_1"}\n'`+"\n"+
				`printf '{"type":"turn.started"}\n'`+"\n"+
				`printf '{"type":"item.started","item":{"id":"i1","type":"file_change","changes":[{"path":"erun-common/mcp_client.go","kind":"update"}]}}\n'`+"\n"+
				`printf '{"type":"item.completed","item":{"id":"i1","type":"file_change","changes":[{"path":"erun-common/mcp_client.go","kind":"update"}]}}\n'`+"\n"+
				`printf '{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"Fixed the client."}}\n'`+"\n"+
				`printf '{"type":"turn.completed"}\n'`+"\n"+
				"exit 0")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "codex")...))

		start := startJob(t, setup, envVars, "sweep", "--agent", "codex", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/agent_progress_is_the_same_shape_for_every_tool", normalize.Apply(status.Combined))

		// The normalized fields are what an orchestrator branches on, and the
		// snapshot above renders them as one line; the record is the contract.
		body, err := os.ReadFile(jobRecordPath(setup, "team", "dev", "sweep"))
		if err != nil {
			t.Fatalf("read job record: %v", err)
		}
		var record struct {
			Kind      string `json:"kind"`
			AgentTool string `json:"agentTool"`
			Progress  *struct {
				Activity   string `json:"activity"`
				LastTool   string `json:"lastTool"`
				LastTarget string `json:"lastTarget"`
				ToolsRun   int    `json:"toolsRun"`
				Turns      int    `json:"turns"`
				Result     string `json:"result"`
			} `json:"progress"`
		}
		if err := json.Unmarshal(body, &record); err != nil {
			t.Fatalf("parse job record: %v\n%s", err, body)
		}
		if record.Kind != "agent" || record.AgentTool != "codex" || record.Progress == nil {
			t.Fatalf("expected an agent record carrying progress, got:\n%s", body)
		}
		if record.Progress.LastTool != "Edit" || record.Progress.LastTarget != "erun-common/mcp_client.go" {
			t.Errorf("codex file_change must normalize to the same tool/target a claude edit does, got %+v", *record.Progress)
		}
		if record.Progress.ToolsRun != 1 || record.Progress.Turns != 1 || record.Progress.Result != "Fixed the client." {
			t.Errorf("unexpected normalized progress: %+v", *record.Progress)
		}
	})

	t.Run("await_reports_a_timeout_distinctly_from_a_failure", func(t *testing.T) {
		// The defect this whole surface closes: an orchestrator that cannot tell
		// "not finished yet" from "failed". The bounded wait exits 124 with the job
		// still running, where a failed job exits 1 (see the non-zero scenario
		// below) — two different events, two different codes, neither inferred from
		// the other.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		release := filepath.Join(setup.Cwd, "release")
		envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

		start := startJob(t, setup, envVars, "slow", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "slow", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 124 {
			t.Fatalf("expected the timeout exit code 124, got %d:\n%s", await.ExitCode, await.Combined)
		}
		golden.Equal(t, "job/await_reports_a_timeout_distinctly_from_a_failure", normalize.Apply(await.Combined))

		// timedOut is carried in the result too, so a caller reading the payload
		// does not have to infer it from an exit code either. It is masked by
		// normalization, so it is asserted from the parsed JSON.
		awaitJSON := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "slow", "--timeout", "1s", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			TimedOut bool `json:"timedOut"`
			Job      struct {
				State    string `json:"state"`
				ExitCode *int   `json:"exitCode"`
			} `json:"job"`
		}
		if err := json.Unmarshal([]byte(awaitJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse await JSON: %v\n%s", err, awaitJSON.Stdout)
		}
		if !payload.TimedOut || payload.Job.State != "running" {
			t.Errorf("expected a timed-out running job, got %+v", payload)
		}
		if payload.Job.ExitCode != nil {
			t.Errorf("a running job must carry no exit code, got %d", *payload.Job.ExitCode)
		}

		// Let the work finish so the scenario leaves nothing running, and confirm
		// the same await now reports the outcome rather than a timeout.
		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		done := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "slow", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if done.ExitCode != 0 {
			t.Fatalf("expected exit 0 once the work succeeded, got %d:\n%s", done.ExitCode, done.Combined)
		}
	})

	t.Run("status_reports_a_non_zero_exit_captured_in_the_environment", func(t *testing.T) {
		// The exit status is observed by the supervisor waiting on the work, so it
		// survives with no sentinel token in the log and no shell between the work
		// and its result. The stub prints on both streams to lock that they are
		// captured together.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "printf 'compiling\\n'\nprintf 'boom\\n' >&2\nexit 42")

		start := startJob(t, setup, envVars, "suite", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected a failed job to exit 1, distinct from the 124 timeout, got %d:\n%s", await.ExitCode, await.Combined)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		output := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "suite"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/status_reports_a_non_zero_exit_captured_in_the_environment", normalize.Apply(
			await.Combined+status.Combined+output.Combined))

		// The captured code is the contract; normalization leaves it alone, but the
		// persisted record is a side effect outside the streams, so it is asserted
		// on disk.
		body, err := os.ReadFile(jobRecordPath(setup, "team", "dev", "suite"))
		if err != nil {
			t.Fatalf("read job record: %v", err)
		}
		var record struct {
			State    string `json:"state"`
			ExitCode *int   `json:"exitCode"`
		}
		if err := json.Unmarshal(body, &record); err != nil {
			t.Fatalf("parse job record: %v\n%s", err, body)
		}
		if record.State != "exited" || record.ExitCode == nil || *record.ExitCode != 42 {
			t.Errorf("expected the job record to carry the captured exit 42, got %s", body)
		}
	})

	t.Run("status_names_why_a_failed_job_failed_instead_of_only_its_exit_code", func(t *testing.T) {
		// Every other terminal status line already rendered the recorded reason;
		// exited did not, so a job that never even started reported a bare
		// "exited -1" and left the one field that says what went wrong readable
		// only in JSON. The work here is an absolute path that does not exist, so
		// the supervisor's own start fails and the reason is the whole answer.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := inEnvironment(setup.Env())

		start := startJob(t, setup, envVars, "cannot-start", "--", "/erun-integration/no-such-work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "cannot-start", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "cannot-start"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/status_names_why_a_failed_job_failed", normalize.Apply(await.Combined+status.Combined))
	})

	t.Run("output_is_readable_while_the_job_runs", func(t *testing.T) {
		// Progress must be visible before the work exits, not buffered to the end,
		// and the next offset must let a poll continue rather than repeat. The stub
		// signals when its first line is flushed and waits to be released, so the
		// scenario reads mid-run on an observable condition rather than a sleep.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		flushed := filepath.Join(setup.Cwd, "flushed")
		release := filepath.Join(setup.Cwd, "release")
		envVars := jobStubEnv(t, setup,
			"printf 'first\\n'\n"+
				"printf '"+jobStubSignal+"' > '"+flushed+"'\n"+
				"while [ ! -f '"+release+"' ]; do sleep 0.05; done\n"+
				"printf 'second\\n'\n"+
				"exit 0")

		start := startJob(t, setup, envVars, "chatty", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		waitForFile(t, flushed, 30*time.Second)

		midRun := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "chatty"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if midRun.ExitCode != 0 {
			t.Fatalf("mid-run output: exit %d: %s", midRun.ExitCode, midRun.Combined)
		}
		if midRun.Stdout != "first\n" {
			t.Fatalf("expected only the flushed first line mid-run, got %q", midRun.Stdout)
		}

		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "chatty", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		// Resuming from the mid-run read's next offset returns only what was
		// written after it.
		rest := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "chatty", "--offset", "6"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/output_is_readable_while_the_job_runs", normalize.Apply(
			midRun.Combined+rest.Combined))
	})

	t.Run("a_running_job_holds_an_activity_lease", func(t *testing.T) {
		// The payoff of building jobs on the lease store: starting work makes the
		// environment read as busy, and finishing it releases the claim, without
		// the caller arranging either.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		release := filepath.Join(setup.Cwd, "release")
		envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

		start := startJob(t, setup, envVars, "indexing", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		held := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})

		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "indexing", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		released := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "job/a_running_job_holds_an_activity_lease", normalize.Apply(held.Combined+released.Combined))
	})

	t.Run("cancel_signals_the_recorded_process_and_records_the_outcome", func(t *testing.T) {
		// The cancel targets a pid the record holds, so it can never match the
		// shell issuing it. The supervisor is left alone, which is why the
		// cancelled job reads back as a normal exited job carrying the signal
		// rather than as a job whose outcome nobody recorded.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		started := filepath.Join(setup.Cwd, "started")
		envVars := jobStubEnv(t, setup, "printf '"+jobStubSignal+"' > '"+started+"'\nwhile true; do sleep 0.05; done")

		start := startJob(t, setup, envVars, "runaway", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		waitForFile(t, started, 30*time.Second)

		cancel := erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "runaway"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if cancel.ExitCode != 0 {
			t.Fatalf("cancel: exit %d: %s", cancel.ExitCode, cancel.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "runaway", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected a cancelled job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
		}
		golden.Equal(t, "job/cancel_signals_the_recorded_process_and_records_the_outcome", normalize.Apply(cancel.Combined+await.Combined))
	})

	t.Run("an_abandoned_job_reads_as_unknown_never_as_success", func(t *testing.T) {
		// A record whose supervisor is gone without an outcome — what a replaced
		// runtime pod leaves behind. Seeded directly, because killing a supervisor
		// mid-run would race the outcome it is about to write, and the state under
		// test is the record, not the kill. A pid of 1 that erun is not is a
		// process the record can be reconciled against; the seeded pid below is one
		// that cannot exist.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "stranded"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		record := `{
  "id": "stranded",
  "name": "overnight build",
  "state": "running",
  "pid": 2147483646,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-stranded"
}
`
		if err := os.WriteFile(filepath.Join(dir, "stranded.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "stranded"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "stranded", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if await.ExitCode != 125 {
			t.Fatalf("expected the unknown-outcome exit code 125, distinct from both 0 and 124, got %d:\n%s", await.ExitCode, await.Combined)
		}
		golden.Equal(t, "job/an_abandoned_job_reads_as_unknown_never_as_success", normalize.Apply(status.Combined+await.Combined))

		// The demotion is persisted, so every later read gives the same definite
		// answer instead of re-deciding it.
		body, err := os.ReadFile(filepath.Join(dir, "stranded.json"))
		if err != nil {
			t.Fatalf("read reconciled record: %v", err)
		}
		if !strings.Contains(string(body), `"state": "unknown"`) {
			t.Errorf("expected the reconciled record to persist the unknown state, got:\n%s", body)
		}
	})

	t.Run("an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_subprocess", func(t *testing.T) {
		// A stranded job whose recorded hostname matches this pod's own
		// hostname rules out a pod replacement, so reconciliation asks
		// Kubernetes whether the runtime container itself restarted
		// (jobSupervisorContainerRestart, kubectl-pod-get execution mode).
		// The kubectl stub answers `get pod <hostname> -o json` with a
		// terminated erun-devops container, and the reported reason must name
		// the real cause instead of falling back to "could not be
		// determined".
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		hostname, err := os.Hostname()
		if err != nil {
			t.Fatalf("os.Hostname: %v", err)
		}
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "restarted"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		record := fmt.Sprintf(`{
  "id": "restarted",
  "name": "overnight build",
  "state": "running",
  "pid": 2147483645,
  "hostname": %q,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-restarted"
}
`, hostname)
		if err := os.WriteFile(filepath.Join(dir, "restarted.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`case "$*" in`,
			`  *"get pod "*) printf '%s\n' '{"status":{"containerStatuses":[{"name":"erun-devops","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"2026-01-01T00:05:00Z"}}}]}}' ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...))

		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "restarted"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "restarted", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 125 {
			t.Fatalf("expected the unknown-outcome exit code 125, got %d:\n%s", await.ExitCode, await.Combined)
		}
		golden.Equal(t, "job/an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_subprocess", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("an_abandoned_job_on_the_same_pod_names_a_container_restart_via_kubectl_pod_get_library_execution_mode", func(t *testing.T) {
		// Proves the library path (kubectl-pod-get=library) produces the same
		// observable result as the subprocess path above -- a real client-go
		// call against a fake API server instead of shelling out to kubectl.
		// The kubectl stub fails loudly and distinctively only for a "get
		// pod" argv, so a fallback to the subprocess path surfaces as a
		// recognizable failure instead of silently passing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedExecutionMode(t, setup, "kubectl-pod-get", "library")
		hostname, err := os.Hostname()
		if err != nil {
			t.Fatalf("os.Hostname: %v", err)
		}
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "restarted"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		record := fmt.Sprintf(`{
  "id": "restarted",
  "name": "overnight build",
  "state": "running",
  "pid": 2147483645,
  "hostname": %q,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-restarted"
}
`, hostname)
		if err := os.WriteFile(filepath.Join(dir, "restarted.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}

		var apiHits atomic.Int64
		podPath := "/api/v1/namespaces/team-dev/pods/" + hostname
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiHits.Add(1)
			if r.URL.Path != podPath {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":{"containerStatuses":[{"name":"erun-devops","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"2026-01-01T00:05:00Z"}}}]}}`)
		}))
		defer apiServer.Close()
		kubeDir := filepath.Join(setup.Home, ".kube")
		if err := os.MkdirAll(kubeDir, 0o700); err != nil {
			t.Fatalf("mkdir .kube: %v", err)
		}
		kubeconfig := "apiVersion: v1\n" +
			"kind: Config\n" +
			"clusters:\n" +
			"  - name: test-cluster\n" +
			"    cluster:\n" +
			"      server: " + apiServer.URL + "\n" +
			"contexts:\n" +
			"  - name: test-context\n" +
			"    context:\n" +
			"      cluster: test-cluster\n" +
			"      user: test-user\n" +
			"      namespace: team-dev\n" +
			"users:\n" +
			"  - name: test-user\n" +
			"    user:\n" +
			"      token: test-token\n" +
			"current-context: test-context\n"
		if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`case "$*" in`,
			`  *"get pod "*) printf '%s\n' "fell through to the kubectl subprocess for the container restart check" >&2; exit 1 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...))

		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "restarted"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		if strings.Contains(status.Combined, "fell through to the kubectl subprocess") {
			t.Fatalf("container restart check used the kubectl subprocess despite library execution mode:\n%s", status.Combined)
		}
		if got := apiHits.Load(); got == 0 {
			t.Fatalf("fake API server received no requests; library path never ran")
		}
		if !strings.Contains(status.Combined, "OOMKilled") {
			t.Fatalf("expected the library path to report the same OOMKilled restart the subprocess path does:\n%s", status.Combined)
		}
	})

	t.Run("a_job_that_backgrounds_work_and_exits_reads_as_abandoned_not_success", func(t *testing.T) {
		// The supervisor waits on its immediate child only; a child that
		// backgrounds a grandchild and exits 0 would otherwise read as a clean
		// success while the backgrounded work runs on unsupervised. Redirecting
		// the grandchild's output away from the supervisor's captured pipe is
		// what reproduces the shape -- a grandchild inheriting that pipe would
		// block the immediate child's own exit instead.
		if runtime.GOOS == "windows" {
			t.Skip("process-group survivor detection is POSIX-only; see erun-common/job_process_windows.go")
		}
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		backgroundLog := filepath.Join(setup.Cwd, "background.log")
		envVars := jobStubEnv(t, setup, "sleep 5 </dev/null >'"+backgroundLog+"' 2>&1 &\nexit 0")

		start := startJob(t, setup, envVars, "gate", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "gate"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "gate", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected an abandoned job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
		}
		// erun#1731: a raw exitCode of 0 sitting beside state "abandoned" is
		// exactly the false-success shape a caller reading only exitCode would
		// miss; succeeded must say so explicitly rather than leaving it to be
		// re-derived from state.
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "gate", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State     string `json:"state"`
			ExitCode  *int   `json:"exitCode"`
			Succeeded bool   `json:"succeeded"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "abandoned" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.Succeeded {
			t.Fatalf("expected an abandoned job with exitCode 0 to report succeeded=false, got %+v", payload)
		}
		golden.Equal(t, "job/a_job_that_backgrounds_work_and_exits_reads_as_abandoned_not_success", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("a_job_that_ends_while_a_job_it_started_is_still_running_reads_as_gate_incomplete", func(t *testing.T) {
		// The reproduction this closes: an agent job runs a gate through its own
		// `job start` (exactly what agent-gate.sh's detach-and-await does from
		// inside an agent's Bash tool) and then ends -- by backgrounding itself,
		// running out of turns, or an outer `timeout` killing the foreground
		// wait -- while the gate has not reached a verdict. The gate is a
		// separate, detached job on purpose, so it is never a member of the
		// outer job's process group and the abandoned scenario above cannot see
		// it; only the StartedByJobID record relationship can. The nested
		// `job start` here runs through the real compiled binary rather than a
		// seeded record, so this locks the whole wiring end to end: the outer
		// job's supervisor has to actually propagate ERUN_JOB_ID to the "work"
		// stub's process, and the stub's own nested erun call has to actually
		// read it back onto the gate job it starts.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		// The nested job start's own stdout (a "job started: ..." line naming its
		// log path) is discarded rather than left to land in outer's captured
		// output: the log path's length varies with the test's own tempdir name,
		// which would make outer's recorded outputBytes -- and so the golden --
		// flaky across runs.
		script := fmt.Sprintf("%q job start --tenant team --environment dev --name gate --id gate -- sleep 30 >/dev/null 2>&1\nexit 0\n", bin)
		// The outer job's own finish check now waits for the gate to reach a
		// verdict (see resolveEnvironmentJobOutcome) before it decides
		// gate-incomplete -- the gate here sleeps far longer than this scenario
		// could afford to wait for real, so the cap/poll are shrunk to
		// milliseconds. The point under test is the eventual gate-incomplete
		// outcome once the wait is exhausted, not genuinely waiting one out.
		envVars := append(jobStubEnv(t, setup, script),
			"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=100ms", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms")

		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "gate", "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		})

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected a gate-incomplete job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
		}
		// erun#1731: this is the exact shape the issue reported -- exitCode 0
		// sitting beside state gate-incomplete, with only state telling the
		// truth. succeeded must say so explicitly, so exec_agent (and any other
		// caller reading the job record over MCP or --output json) cannot read
		// this as success from exitCode alone.
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State     string `json:"state"`
			ExitCode  *int   `json:"exitCode"`
			Succeeded bool   `json:"succeeded"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "gate-incomplete" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.Succeeded {
			t.Fatalf("expected a gate-incomplete job with exitCode 0 to report succeeded=false, got %+v", payload)
		}
		golden.Equal(t, "job/a_job_that_ends_while_a_job_it_started_is_still_running_reads_as_gate_incomplete", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("a_job_that_waits_for_a_job_it_started_reports_the_real_outcome_once_it_finishes", func(t *testing.T) {
		// The fix this locks: the scenario above ends because the wait cap is
		// exhausted, but the common case is the started job actually finishing.
		// Here the gate finishes (successfully) well within any reasonable wait,
		// so outer's own finish check waits it out and reports the *real* combined
		// outcome instead of ever surfacing an intermediate gate-incomplete a
		// caller would otherwise have to separately chase down.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		script := fmt.Sprintf("%q job start --tenant team --environment dev --name gate --id gate -- sleep 0.2 >/dev/null 2>&1\nexit 0\n", bin)
		envVars := jobStubEnv(t, setup, script)

		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected outer to report the gate's real success once it finished, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State     string `json:"state"`
			Succeeded bool   `json:"succeeded"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "exited" || !payload.Succeeded {
			t.Fatalf("expected outer to report exited/succeeded once the gate it waited for finished, got %+v", payload)
		}
	})

	t.Run("a_job_that_waits_for_a_failed_job_it_started_surfaces_startedJobFailed", func(t *testing.T) {
		// The other half of the fix above: when the started job finishes but
		// fails, that failure must not vanish behind outer's own clean exit code
		// just because outer's process happened to exit 0.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		stubs := filepath.Join(setup.Cwd, "stubs")
		// The scrubbed PATH (see AGENTS.md "The PATH is scrubbed") has no `sh`
		// forwarder, so a nested job whose own command was `sh -c '...'` failed
		// to start rather than genuinely running its command -- the loose
		// assertion below (matching "gate" in startedJobFailed regardless of
		// why it failed) never caught it. A declared stub is the real gate
		// command instead, so this locks the actual "started job finished with
		// a failing exit code" path, not an accidental "sh is unresolvable" one.
		fixture.StubBinaryWithScript(t, stubs, "gatefail", "sleep 0.2\nexit 1\n")
		script := fmt.Sprintf("%q job start --tenant team --environment dev --name gate --id gate -- gatefail >/dev/null 2>&1\nexit 0\n", bin)
		envVars := append(jobStubEnv(t, setup, script), fixture.StubEnv(stubs, "gatefail")...)

		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected outer to report a failure once the gate it waited for failed, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State            string `json:"state"`
			Succeeded        bool   `json:"succeeded"`
			StartedJobFailed string `json:"startedJobFailed"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "exited" || payload.Succeeded {
			t.Fatalf("expected outer to still report its own exited state but succeeded=false, got %+v", payload)
		}
		if !strings.Contains(payload.StartedJobFailed, "gate") {
			t.Fatalf("expected startedJobFailed to name the failed gate job, got %+v", payload)
		}
	})

	t.Run("startedJobFailed_ignores_a_superseded_earlier_attempt_under_the_same_name", func(t *testing.T) {
		// agent-gate.sh folds the tree and command into the nested job's --id,
		// so an agent that fixes what a gate found and reruns it gets a fresh
		// id under the same --name -- the earlier failing attempt's record is
		// never replaced (only reusing an id outright does that). Without
		// accounting for that, a parent would keep naming the stale failure
		// even after the same-named gate went green: exited/succeeded sitting
		// next to a startedJobFailed naming a job that was not the one that
		// actually ran last.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "gate1cmd", "exit 1\n")
		fixture.StubBinaryWithScript(t, stubs, "gate2cmd", "exit 0\n")
		script := fmt.Sprintf(
			"%q job start --tenant team --environment dev --name gate --id gate-1 -- gate1cmd >/dev/null 2>&1\n"+
				"%q job await --tenant team --environment dev --id gate-1 --timeout 10s >/dev/null 2>&1\n"+
				"%q job start --tenant team --environment dev --name gate --id gate-2 -- gate2cmd >/dev/null 2>&1\n"+
				"%q job await --tenant team --environment dev --id gate-2 --timeout 10s >/dev/null 2>&1\n"+
				"exit 0\n",
			bin, bin, bin, bin)
		envVars := append(jobStubEnv(t, setup, script), fixture.StubEnv(stubs, "gate1cmd", "gate2cmd")...)

		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			for _, id := range []string{"gate-1", "gate-2"} {
				erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
					erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			}
		})
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected outer to report success once the latest same-named attempt passed, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State            string `json:"state"`
			Succeeded        bool   `json:"succeeded"`
			StartedJobFailed string `json:"startedJobFailed"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "exited" || !payload.Succeeded || payload.StartedJobFailed != "" {
			t.Fatalf("expected outer to report exited/succeeded with no startedJobFailed once the latest attempt passed, got %+v", payload)
		}
	})

	t.Run("a_job_that_ends_while_a_background_task_job_it_started_is_still_running_reads_as_gate_incomplete", func(t *testing.T) {
		// Task jobs -- what build/deploy/doctor and the rest of the job-envelope
		// tools become when an MCP caller sets wait:false -- run in the MCP
		// server's own long-lived process, never as a subprocess of this binary,
		// so nothing in this suite can start one directly. Seeding the record it
		// would have produced (kind "task", a real alive pid so reconciliation
		// reads it as genuinely running rather than demoting it to unknown,
		// startedByJobId naming the parent) is what proves the parent's own
		// finish check -- previously blind to task jobs because none of them
		// ever wrote startedByJobId at all -- now finds one exactly like it
		// finds a nested command job (see the gate-incomplete scenario above).
		// The seeded pid is this test binary's own: it is a real, alive process
		// for the run's whole duration, so the child genuinely reads as running
		// rather than as an abandoned/unknown record the way a fabricated dead
		// pid would.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "task-child"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		logPath := filepath.Join(dir, "task-child.log")
		if err := os.WriteFile(logPath, []byte("building...\n"), 0o644); err != nil {
			t.Fatalf("seed task log: %v", err)
		}
		record := fmt.Sprintf(`{
  "id": "task-child",
  "name": "task-child",
  "state": "running",
  "kind": "task",
  "pid": %d,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-task-child",
  "startedByJobId": "outer",
  "logPath": %q
}
`, os.Getpid(), logPath)
		if err := os.WriteFile(filepath.Join(dir, "task-child.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}

		// The seeded task job never finishes on its own, so the wait/poll are
		// shrunk to milliseconds the same way the nested-job gate-incomplete
		// scenario above does -- the point under test is the eventual
		// gate-incomplete outcome once the wait is exhausted, not genuinely
		// waiting one out.
		envVars := append(jobStubEnv(t, setup, "exit 0"),
			"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=100ms", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms")
		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected a gate-incomplete job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State     string `json:"state"`
			ExitCode  *int   `json:"exitCode"`
			Succeeded bool   `json:"succeeded"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "gate-incomplete" || payload.ExitCode == nil || *payload.ExitCode != 0 || payload.Succeeded {
			t.Fatalf("expected a gate-incomplete job with exitCode 0 to report succeeded=false, got %+v", payload)
		}
		if !strings.Contains(payload.Reason, "task-child") {
			t.Fatalf("expected the gate-incomplete reason to name the task job it started, got %+v", payload)
		}

		child := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "task-child", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var childPayload struct {
			Kind           string `json:"kind"`
			StartedByJobID string `json:"startedByJobId"`
		}
		if err := json.Unmarshal([]byte(child.Stdout), &childPayload); err != nil {
			t.Fatalf("parse task job status JSON: %v\n%s", err, child.Stdout)
		}
		if childPayload.Kind != "task" || childPayload.StartedByJobID != "outer" {
			t.Fatalf("expected the task job to render its own kind and parent, got %+v", childPayload)
		}
	})

	t.Run("a_job_that_waits_for_a_failed_background_task_job_it_started_surfaces_startedJobFailed_and_leaves_its_log_readable", func(t *testing.T) {
		// The other half of the fix: a task job that already failed before the
		// parent's own process even exits is picked up by the same
		// environmentJobFailedChildren scan a nested command job's failure is.
		// A task job is a Go call with no subprocess stdio to capture, so what a
		// poll can read back is only whatever the work itself wrote to the log
		// it was handed -- before this PR a failed task left nothing at all to
		// read, just a bare exit code and a bare error string.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "task-fail"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		logPath := filepath.Join(dir, "task-fail.log")
		if err := os.WriteFile(logPath, []byte("pushing image...\nauth failed\n"), 0o644); err != nil {
			t.Fatalf("seed task log: %v", err)
		}
		record := fmt.Sprintf(`{
  "id": "task-fail",
  "name": "task-fail",
  "state": "exited",
  "kind": "task",
  "pid": 123456789,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": 1,
  "reason": "push failed: authentication required",
  "leaseId": "job-task-fail",
  "startedByJobId": "outer",
  "logPath": %q
}
`, logPath)
		if err := os.WriteFile(filepath.Join(dir, "task-fail.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}

		envVars := jobStubEnv(t, setup, "exit 0")
		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected outer to report a failure once the task job it started failed, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State            string `json:"state"`
			Succeeded        bool   `json:"succeeded"`
			StartedJobFailed string `json:"startedJobFailed"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.State != "exited" || payload.Succeeded {
			t.Fatalf("expected outer to still report its own exited state but succeeded=false, got %+v", payload)
		}
		if !strings.Contains(payload.StartedJobFailed, "task-fail") {
			t.Fatalf("expected startedJobFailed to name the failed task job, got %+v", payload)
		}

		// The failed task job's own status line renders the recorded reason
		// (jobExitedLine) exactly as a failed command job's does, and its log --
		// the one thing a task job has no subprocess stdio to fall back on -- is
		// readable through the same `job output` path as any other kind.
		childStatus := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "task-fail"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(childStatus.Combined, "authentication required") {
			t.Fatalf("expected the task job's own status line to name its recorded reason, got: %s", childStatus.Combined)
		}
		output := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "task-fail"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(output.Stdout, "auth failed") {
			t.Fatalf("expected the task job's log to be readable via job output, got: %s", output.Combined)
		}
	})

	t.Run("a_task_job_started_with_no_parent_stays_parentless", func(t *testing.T) {
		// An orchestrator driving this environment from outside any job at all
		// -- never itself started as a job, so it has nothing to thread as
		// startedByJobId -- is exactly the caller TaskEnvironmentJobParams'
		// own doc describes: empty when nothing started it, never a guess at
		// whichever job happens to be running here. Seeded with no
		// startedByJobId key at all (the shape StartTaskEnvironmentJob itself
		// produces for that caller, since the field is omitempty), this proves
		// two things: the task job's own record reports no parent, and a real,
		// unrelated job running in the same environment at the same time is
		// not wrongly credited with having started it merely because it
		// happens to be the job running here.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "task-orphan"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		record := fmt.Sprintf(`{
  "id": "task-orphan",
  "name": "task-orphan",
  "state": "running",
  "kind": "task",
  "pid": %d,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-task-orphan"
}
`, os.Getpid())
		if err := os.WriteFile(filepath.Join(dir, "task-orphan.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}

		envVars := jobStubEnv(t, setup, "exit 0")
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "task-orphan", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			StartedByJobID string `json:"startedByJobId"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		if payload.StartedByJobID != "" {
			t.Fatalf("expected a task job started with no parent to record none, got %+v", payload)
		}

		start := startJob(t, setup, envVars, "unrelated", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "unrelated", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected the unrelated job to succeed on its own, not to be blocked on the parentless task job merely running alongside it, got %d:\n%s", await.ExitCode, await.Combined)
		}
	})

	t.Run("an_agent_jobs_started_gate_that_failed_gets_one_bounded_resumed_turn_that_fixes_it", func(t *testing.T) {
		// The supervisor's own wait-for-children mechanism (scenarios above)
		// already tells the truth once an agent job's own turn
		// ends while a gate it started fails -- startedJobFailed names it. What
		// it could not do until now is give the agent itself a real "later" to
		// act on that: nothing ever re-invoked it. The stubbed "claude" binary
		// branches on whether its own argv carries --resume: its first turn
		// starts a failing gate and exits without --resume in its own argv (it
		// cannot see it -- this is the first turn); erun then resumes the same
		// session (verified live against a real claude to actually carry
		// context, see erun-common/AGENTS.md) and hands it the concrete
		// failure. This resumed turn "fixes" it the same way a real agent would
		// after a failing gate: it reruns the gate under the same --name with a
		// fresh --id, which succeeds -- superseding the earlier failing attempt
		// under that name (the same supersede rule the scenario above locks) --
		// before it exits 0. Not starting anything new on resume would not
		// prove a fix: the earlier failed attempt stays the latest (and only)
		// attempt under its name forever unless something supersedes it, so the
		// resumed turn has to actually redo the work, not just report success.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "gatefail", "sleep 0.1\nexit 1\n")
		fixture.StubBinaryWithScript(t, stubs, "gateok", "sleep 0.1\nexit 0\n")
		claudeScript := fmt.Sprintf(`case "$*" in
  *--resume*)
    printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
    %q job start --tenant team --environment dev --name gate --id gate-2 -- gateok >/dev/null 2>&1
    printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"fixed the gate"}\n'
    ;;
  *)
    printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
    %q job start --tenant team --environment dev --name gate --id gate -- gatefail >/dev/null 2>&1
    printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"started the gate"}\n'
    ;;
esac
`, bin, bin)
		fixture.StubBinaryWithScript(t, stubs, "claude", claudeScript)
		envVars := append(append(inEnvironment(setup.Env()), fixture.StubEnv(stubs, "claude", "gatefail", "gateok")...),
			"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=2s", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms")

		start := startJob(t, setup, envVars, "outer", "--agent", "claude", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			for _, id := range []string{"gate", "gate-2"} {
				erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
					erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			}
		})
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected outer to eventually succeed once its bounded resumed turn fixed the gate, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
	})

	t.Run("an_agent_jobs_started_gate_that_keeps_failing_stops_at_the_reinvocation_bound", func(t *testing.T) {
		// The safety half of the scenario above: a resumed turn that starts
		// another failing gate every time must not loop forever. Every turn here
		// (first and every resumed one) starts a new failing gate under a fresh
		// id, so the outer job never finds anything to succeed on -- the bound
		// itself is what has to stop it. ERUN_JOB_MAX_REINVOCATIONS pins the cap
		// to 1 so the scenario does not need to script and assert an arbitrarily
		// long chain to prove the bound is real.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "gatefail", "sleep 0.1\nexit 1\n")
		counter := filepath.Join(setup.Cwd, "turns")
		claudeScript := fmt.Sprintf(`n=$(cat %q 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > %q
printf '{"type":"system","subtype":"init","session_id":"11111111-1111-1111-1111-111111111111"}\n'
%q job start --tenant team --environment dev --name "gate-$n" --id "gate-$n" -- gatefail >/dev/null 2>&1
printf '{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"started gate-%%s\n"}\n' "$n"
`, counter, counter, bin)
		fixture.StubBinaryWithScript(t, stubs, "claude", claudeScript)
		envVars := append(append(inEnvironment(setup.Env()), fixture.StubEnv(stubs, "claude", "gatefail")...),
			"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=2s", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms", "ERUN_JOB_MAX_REINVOCATIONS=1")

		start := startJob(t, setup, envVars, "outer", "--agent", "claude", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			for _, id := range []string{"gate-1", "gate-2", "gate-3"} {
				erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", id, "--signal", "KILL"},
					erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			}
		})
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected outer to still report a failure once the reinvocation bound was exhausted, got %d:\n%s", await.ExitCode, await.Combined)
		}
		statusJSON := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "outer", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var payload struct {
			State             string `json:"state"`
			Succeeded         bool   `json:"succeeded"`
			StartedJobFailed  string `json:"startedJobFailed"`
			Reason            string `json:"reason"`
			ReinvocationCount int    `json:"reinvocationCount"`
		}
		if err := json.Unmarshal([]byte(statusJSON.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, statusJSON.Stdout)
		}
		// Exactly one reinvocation ran (the pinned cap) even though every turn
		// kept failing the same way -- proof the bound stopped the chain rather
		// than the stub running out of ideas on its own.
		if payload.State != "exited" || payload.Succeeded || payload.StartedJobFailed == "" || payload.ReinvocationCount != 1 {
			t.Fatalf("expected outer to stop after exactly one reinvocation, still failing, got %+v", payload)
		}
		if !strings.Contains(payload.Reason, "already resumed 1 time(s)") {
			t.Fatalf("expected the reason to name the exhausted reinvocation bound, got %q", payload.Reason)
		}
	})

	t.Run("handoff_excludes_a_job_from_its_parents_finish_check", func(t *testing.T) {
		// The other side of the contract: a job started with --handoff is meant
		// to outlive its caller on purpose, so it must never be what a parent's
		// own finish check waits for -- unlike the gate above, outer must report
		// its own real success immediately even though the handed-off job is
		// still running (a long sleep standing in for a release or a render).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		bin := erun.BinaryPath(t)
		script := fmt.Sprintf("%q job start --tenant team --environment dev --name released --id released --handoff -- sleep 30 >/dev/null 2>&1\nexit 0\n", bin)
		envVars := jobStubEnv(t, setup, script)

		start := startJob(t, setup, envVars, "outer", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "released", "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		})

		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected outer to succeed on its own despite the handed-off job still running, got %d:\n%s", await.ExitCode, await.Combined)
		}
		released := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "released"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.HasPrefix(released.Combined, "running:") {
			t.Fatalf("expected the handed-off job to still be running on its own after outer finished, got: %s", released.Combined)
		}
	})

	t.Run("an_agent_job_that_ends_with_an_uncommitted_working_tree_is_checkpointed_pushed_and_not_reported_as_success", func(t *testing.T) {
		// The reproduction this closes: an agent's own turn ends after it edited
		// files but before it committed them, and the job's exit code alone reads
		// as a plain success. The agent here is a stubbed "claude" binary (routed
		// through the ERUN_CLAUDE_BIN seam, same as kubectl/helm elsewhere in this
		// suite) that just writes an uncommitted file and exits 0 -- standing in
		// for an agent turn that produced real, uncommitted work. The lane's
		// working tree and remote are real git repositories, not stubs, so "the
		// checkpoint commit actually landed on the remote" is an observable fact.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")

		lane := filepath.Join(setup.Cwd, "lane")
		fixture.SeedGitRepo(t, lane)
		fixture.RunGit(t, lane, "checkout", "-q", "-b", "feature/lane")
		remote := filepath.Join(setup.Home, "lane-origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remote)
		fixture.RunGit(t, lane, "remote", "add", "origin", remote)

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude", "printf 'lane work\\n' > uncommitted.txt\nexit 0\n")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
			"--dir", lane, "--agent", "claude", "--", "do the lane's work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "lane", "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		})

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "lane"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode == 0 {
			t.Fatalf("expected a dirty-worktree job to report a failure outcome, got 0:\n%s", await.Combined)
		}
		// The checkpoint commit itself reached the real remote -- not stubbed,
		// not asserted only through the job record.
		fixture.RunGit(t, remote, "show-ref", "--verify", "--quiet", "refs/heads/feature/lane")
		golden.Equal(t, "job/an_agent_job_that_ends_with_an_uncommitted_working_tree_is_checkpointed_pushed_and_not_reported_as_success", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("an_agent_jobs_clean_pushed_clone_under_the_work_root_is_reclaimed_after_it_finishes", func(t *testing.T) {
		// The reproduction this closes: every agent task clones the repo into
		// /home/erun/work/<name> and nothing ever reclaims it, so the work
		// directory grows without bound (erun#1710). A clone under the work
		// root whose tree is clean and fully pushed has nothing left to lose,
		// so the supervisor removes it the moment the job that owned it exits
		// -- proven here against a real git working tree and a real remote,
		// not just the job record's own claim.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")

		lane := filepath.Join(setup.Home, "work", "erun-w1")
		fixture.SeedGitRepo(t, lane)
		fixture.RunGit(t, lane, "checkout", "-q", "-b", "feature/lane")
		remote := filepath.Join(setup.Home, "lane-origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remote)
		fixture.RunGit(t, lane, "remote", "add", "origin", remote)
		fixture.RunGit(t, lane, "push", "-q", "-u", "origin", "feature/lane")

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude", "exit 0\n")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
			"--dir", lane, "--agent", "claude", "--", "do the lane's work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "lane", "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		})

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "lane"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("expected a clean, fully-pushed job to report success, got %d:\n%s", await.ExitCode, await.Combined)
		}
		// The clone itself is gone from disk -- not asserted only through the
		// job record.
		if _, err := os.Stat(lane); !os.IsNotExist(err) {
			t.Fatalf("clone dir still exists after being reported reclaimed: %v", err)
		}
		golden.Equal(t, "job/an_agent_jobs_clean_pushed_clone_under_the_work_root_is_reclaimed_after_it_finishes", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("an_agent_jobs_dirty_detached_clone_is_kept_and_the_reason_is_named", func(t *testing.T) {
		// The obvious reclaim is destructive: a detached HEAD with
		// uncommitted work is exactly the shape the reported environment's
		// stale clones had (erun#1710). The supervisor must refuse to remove
		// it and say why, rather than leaving an operator to rediscover by
		// hand that a clone still holds real work.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")

		lane := filepath.Join(setup.Home, "work", "erun-w2")
		fixture.SeedGitRepo(t, lane)
		remote := filepath.Join(setup.Home, "lane-origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remote)
		fixture.RunGit(t, lane, "remote", "add", "origin", remote)
		fixture.RunGit(t, lane, "push", "-q", "origin", "main")
		fixture.RunGit(t, lane, "checkout", "-q", "--detach", "HEAD")

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude", "printf 'lane work\\n' > uncommitted.txt\nexit 0\n")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
			"--dir", lane, "--agent", "claude", "--", "do the lane's work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		t.Cleanup(func() {
			erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "lane", "--signal", "KILL"},
				erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		})

		var status erun.Result
		deadline := time.Now().Add(30 * time.Second)
		for {
			status = erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "lane"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if !strings.HasPrefix(status.Combined, "running:") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job never left running: %s", status.Combined)
			}
			time.Sleep(50 * time.Millisecond)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode == 0 {
			t.Fatalf("expected a dirty-worktree job to report a failure outcome, got 0:\n%s", await.Combined)
		}
		if _, err := os.Stat(lane); err != nil {
			t.Fatalf("clone dir was removed even though it should have been kept: %v", err)
		}
		golden.Equal(t, "job/an_agent_jobs_dirty_detached_clone_is_kept_and_the_reason_is_named", normalize.Apply(status.Combined+await.Combined))
	})

	t.Run("a_job_started_on_a_different_pod_reads_back_as_pod_replaced_not_a_guess", func(t *testing.T) {
		// The supervisor stamps the pod's hostname at start, so a record
		// read back from a different hostname is definitive proof of pod
		// replacement, not the older "most likely" guess. Seeded directly for
		// the same reason as the sibling "abandoned" scenario: the state under
		// test is the record's hostname mismatch, not how a real pod eviction
		// would produce one. hostname is a value this suite's own host can never
		// report, so the mismatch is guaranteed.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "evicted"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		record := `{
  "id": "evicted",
  "name": "overnight release",
  "state": "running",
  "pid": 2147483646,
  "startedAt": "2026-01-01T00:00:00Z",
  "exitCode": null,
  "leaseId": "job-evicted",
  "hostname": "team-devops-original-pod-58f9c7d9b6-abcde"
}
`
		if err := os.WriteFile(filepath.Join(dir, "evicted.json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record: %v", err)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "evicted", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		var payload struct {
			State             string `json:"state"`
			UnknownReasonKind string `json:"unknownReasonKind"`
			Reason            string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(status.Stdout), &payload); err != nil {
			t.Fatalf("parse job status JSON: %v\n%s", err, status.Stdout)
		}
		if payload.State != "unknown" {
			t.Fatalf("state = %q, want unknown", payload.State)
		}
		if payload.UnknownReasonKind != "pod-replaced" {
			t.Fatalf("unknownReasonKind = %q, want pod-replaced", payload.UnknownReasonKind)
		}
		if strings.Contains(payload.Reason, "most likely") {
			t.Fatalf("expected a definite reason (hostname mismatch), not a guess, got: %s", payload.Reason)
		}
		if !strings.Contains(payload.Reason, "team-devops-original-pod-58f9c7d9b6-abcde") {
			t.Fatalf("expected the reason to name the pod the job started on, got: %s", payload.Reason)
		}
	})

	t.Run("attach_tracks_a_pid_without_claiming_an_outcome", func(t *testing.T) {
		// Work erun did not start still gets a handle and a lease. The pid seeded
		// here cannot exist, so the job resolves to unknown immediately — which is
		// the honest answer for work nothing erun ran was waiting on.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		attach := erun.Run(t, []string{"job", "attach", "--tenant", "team", "--environment", "dev", "--name", "overnight index", "--id", "overnight", "--pid", "2147483646"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if attach.ExitCode != 0 {
			t.Fatalf("attach: exit %d: %s", attach.ExitCode, attach.Combined)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "overnight"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		golden.Equal(t, "job/attach_tracks_a_pid_without_claiming_an_outcome", normalize.Apply(attach.Combined+status.Combined))
	})

	t.Run("start_refuses_to_reuse_a_running_id", func(t *testing.T) {
		// Two supervisors writing one record would make every later answer
		// ambiguous, so the collision is refused rather than resolved.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		release := filepath.Join(setup.Cwd, "release")
		envVars := jobStubEnv(t, setup, "while [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

		first := startJob(t, setup, envVars, "suite", "--", "work")
		if first.ExitCode != 0 {
			t.Fatalf("first start: exit %d: %s", first.ExitCode, first.Combined)
		}
		second := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--name", "suite", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if second.ExitCode == 0 {
			t.Fatalf("expected the colliding start to fail, got 0:\n%s", second.Combined)
		}
		golden.Equal(t, "job/start_refuses_to_reuse_a_running_id", normalize.Apply(second.Combined))

		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		done := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if done.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", done.ExitCode, done.Combined)
		}
	})

	t.Run("status_of_an_unknown_id_names_the_environment", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "nothing"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an unknown job, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "job/status_of_an_unknown_id_names_the_environment", normalize.Apply(result.Combined))
	})

	t.Run("status_without_jobs_says_so", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/status_without_jobs_says_so", normalize.Apply(result.Combined))
	})

	t.Run("start_requires_target_and_name", func(t *testing.T) {
		setup := env.New(t)
		missingTarget := erun.Run(t, []string{"job", "start", "--name", "suite", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if missingTarget.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", missingTarget.Combined)
		}
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		missingName := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if missingName.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a name, got 0:\n%s", missingName.Combined)
		}
		golden.Equal(t, "job/start_requires_target_and_name", normalize.Apply(missingTarget.Combined+missingName.Combined))
	})

	t.Run("await_refuses_an_unbounded_timeout", func(t *testing.T) {
		// The ceiling is what makes "bounded" real: a caller that wants to wait
		// longer polls again rather than parking on one call.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "2h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a timeout past the ceiling, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "job/await_refuses_an_unbounded_timeout", normalize.Apply(result.Combined))
	})

	t.Run("cancel_dry_run_names_the_target_without_signalling", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		started := filepath.Join(setup.Cwd, "started")
		release := filepath.Join(setup.Cwd, "release")
		envVars := jobStubEnv(t, setup, "printf '"+jobStubSignal+"' > '"+started+"'\nwhile [ ! -f '"+release+"' ]; do sleep 0.05; done\nexit 0")

		start := startJob(t, setup, envVars, "suite", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		waitForFile(t, started, 30*time.Second)
		cancel := erun.Run(t, []string{"job", "cancel", "--tenant", "team", "--environment", "dev", "--id", "suite", "--signal", "KILL", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if cancel.ExitCode != 0 {
			t.Fatalf("cancel dry run: exit %d: %s", cancel.ExitCode, cancel.Combined)
		}
		golden.Equal(t, "job/cancel_dry_run_names_the_target_without_signalling", normalize.Apply(cancel.Combined))

		// The dry run must not have stopped anything: the job is still running and
		// finishes on its own.
		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		done := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if done.ExitCode != 0 {
			t.Fatalf("expected the dry-run-cancelled job to finish normally, got %d:\n%s", done.ExitCode, done.Combined)
		}
	})

	t.Run("output_past_the_cap_is_reported_as_truncated", func(t *testing.T) {
		// A bounded log must never read as a complete answer, and the outcome must
		// survive the bound — which it does, because the exit status never comes
		// from the log.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := jobStubEnv(t, setup, "i=0\nwhile [ $i -lt 200 ]; do printf 'chatter %s\\n' $i; i=$((i+1)); done\nexit 3")

		start := startJob(t, setup, envVars, "chatterbox", "--max-output-bytes", "32", "--", "work")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "chatterbox", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 1 {
			t.Fatalf("expected the captured non-zero exit to survive the output cap, got %d:\n%s", await.ExitCode, await.Combined)
		}
		golden.Equal(t, "job/output_past_the_cap_is_reported_as_truncated", normalize.Apply(await.Combined))
	})

	t.Run("agent_progress_keeps_moving_after_the_output_cap", func(t *testing.T) {
		// The defect this closes: once outputTruncated flips, progress used to
		// freeze at whatever it last read, because it was derived from the same
		// capped log the raw output writer had stopped growing. The cap is set
		// small enough that the very first event already exceeds it, so every
		// later event proves progress is fed straight from the stream rather than
		// by re-reading the (now-frozen) log file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		flushed := filepath.Join(setup.Cwd, "flushed")
		release := filepath.Join(setup.Cwd, "release")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "claude",
			`printf '{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"erun-common/mcp_client.go"}}]}}\n'`+"\n"+
				"printf '"+jobStubSignal+"' > '"+flushed+"'\n"+
				"while [ ! -f '"+release+"' ]; do sleep 0.05; done\n"+
				`printf '{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}\n'`+"\n"+
				`printf '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Fixed the client."}\n'`+"\n"+
				"exit 0")
		envVars := inEnvironment(append(setup.Env(), fixture.StubEnv(stubs, "claude")...))

		start := startJob(t, setup, envVars, "sweep", "--max-output-bytes", "8", "--agent", "claude", "--", "fix the failing tests")
		if start.ExitCode != 0 {
			t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
		}
		waitForFile(t, flushed, 30*time.Second)
		waitForJobActivity(t, setup, envVars, "sweep", 30*time.Second)

		midRun := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var midPayload struct {
			OutputTruncated bool `json:"outputTruncated"`
			Progress        struct {
				Activity string `json:"activity"`
			} `json:"progress"`
		}
		if err := json.Unmarshal([]byte(midRun.Stdout), &midPayload); err != nil {
			t.Fatalf("parse mid-run job status JSON: %v\n%s", err, midRun.Stdout)
		}
		if !midPayload.OutputTruncated {
			t.Fatalf("expected the 8-byte cap to already be hit by the first event, got %s", midRun.Stdout)
		}
		if !strings.Contains(midPayload.Progress.Activity, "erun-common/mcp_client.go") {
			t.Fatalf("expected progress to reflect the first tool call despite the cap, got %q", midPayload.Progress.Activity)
		}

		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatalf("release the stub: %v", err)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--timeout", "30s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if await.ExitCode != 0 {
			t.Fatalf("await: exit %d: %s", await.ExitCode, await.Combined)
		}
		finished := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "sweep", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		var finishedPayload struct {
			OutputTruncated bool `json:"outputTruncated"`
			Progress        struct {
				Turns  int    `json:"turns"`
				Result string `json:"result"`
			} `json:"progress"`
		}
		if err := json.Unmarshal([]byte(finished.Stdout), &finishedPayload); err != nil {
			t.Fatalf("parse finished job status JSON: %v\n%s", err, finished.Stdout)
		}
		if !finishedPayload.OutputTruncated {
			t.Fatalf("expected the job to still report outputTruncated at the end, got %s", finished.Stdout)
		}
		// This is the assertion the bug report's freeze would fail: the second
		// tool call and the final result both arrived well after the 8-byte cap,
		// so seeing them here proves progress kept moving past it.
		if finishedPayload.Progress.Turns != 2 {
			t.Fatalf("expected both turns folded despite the cap, got %+v", finishedPayload.Progress)
		}
		if finishedPayload.Progress.Result != "Fixed the client." {
			t.Fatalf("expected the closing result folded despite the cap, got %+v", finishedPayload.Progress)
		}
	})
}
