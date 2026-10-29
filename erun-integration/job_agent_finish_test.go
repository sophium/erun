package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testJobAnAgentJobsStartedGateThatFailedGetsOneBoundedResumedTurnThatFixesIt(t *testing.T) {
	// An agent's first turn starts a failing gate; erun resumes the session once
	// with the failure, and the resumed turn reruns the gate under the same name
	// (superseding the failure), so the outer job succeeds.
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
			stopJob(t, setup, envVars, id)
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
}

func testJobAnAgentJobsStartedGateThatKeepsFailingStopsAtTheReinvocationBound(t *testing.T) {
	// A resumed turn that keeps starting failing gates stops at the reinvocation
	// bound (pinned to 1).
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
			stopJob(t, setup, envVars, id)
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
	if payload.State != "exited" || payload.Succeeded || payload.StartedJobFailed == "" || payload.ReinvocationCount != 1 {
		t.Fatalf("expected outer to stop after exactly one reinvocation, still failing, got %+v", payload)
	}
	if !strings.Contains(payload.Reason, "already resumed 1 time(s)") {
		t.Fatalf("expected the reason to name the exhausted reinvocation bound, got %q", payload.Reason)
	}
}

func testJobAnAgentJobThatEndsWithAnUncommittedWorkingTreeIsCheckpointedPushedAndNotReportedAsSuccess(t *testing.T) {
	// An agent that exits 0 with uncommitted work is checkpointed, pushed to the
	// real remote, and reported as a failure.
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

	start := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
		"--dir", lane, "--agent", "claude", "--", "do the lane's work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		stopJob(t, setup, envVars, "lane")
	})

	status := waitForJobToLeaveRunning(t, setup, envVars, "lane")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode == 0 {
		t.Fatalf("expected a dirty-worktree job to report a failure outcome, got 0:\n%s", await.Combined)
	}
	fixture.RunGit(t, remote, "show-ref", "--verify", "--quiet", "refs/heads/feature/lane")
	golden.Equal(t, "job/an_agent_job_that_ends_with_an_uncommitted_working_tree_is_checkpointed_pushed_and_not_reported_as_success", normalize.Apply(status.Combined+await.Combined))
}

func testJobAnAgentJobsCleanPushedCloneUnderTheWorkRootIsReclaimedAfterItFinishes(t *testing.T) {
	// A clean, fully pushed clone under the work root is removed once its job exits.
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

	start := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
		"--dir", lane, "--agent", "claude", "--", "do the lane's work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		stopJob(t, setup, envVars, "lane")
	})

	status := waitForJobToLeaveRunning(t, setup, envVars, "lane")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("expected a clean, fully-pushed job to report success, got %d:\n%s", await.ExitCode, await.Combined)
	}
	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Fatalf("clone dir still exists after being reported reclaimed: %v", err)
	}
	golden.Equal(t, "job/an_agent_jobs_clean_pushed_clone_under_the_work_root_is_reclaimed_after_it_finishes", normalize.Apply(status.Combined+await.Combined))
}

func testJobAnAgentJobsDirtyDetachedCloneIsKeptAndTheReasonIsNamed(t *testing.T) {
	// A dirty detached clone under the work root is kept, and the reason is named.
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

	start := erun.Run(t, []string{
		"job", "start", "--tenant", "team", "--environment", "dev", "--name", "lane",
		"--dir", lane, "--agent", "claude", "--", "do the lane's work",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		stopJob(t, setup, envVars, "lane")
	})

	status := waitForJobToLeaveRunning(t, setup, envVars, "lane")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "lane", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode == 0 {
		t.Fatalf("expected a dirty-worktree job to report a failure outcome, got 0:\n%s", await.Combined)
	}
	if _, err := os.Stat(lane); err != nil {
		t.Fatalf("clone dir was removed even though it should have been kept: %v", err)
	}
	golden.Equal(t, "job/an_agent_jobs_dirty_detached_clone_is_kept_and_the_reason_is_named", normalize.Apply(status.Combined+await.Combined))
}
