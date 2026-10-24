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

func testJobAJobThatEndsWhileAJobItStartedIsStillRunningReadsAsGateIncomplete(t *testing.T) {
	// A job that exits 0 while a gate it started through the real binary is still
	// running reads as gate-incomplete; only the StartedByJobID link (via
	// ERUN_JOB_ID) can see the detached gate.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	bin := erun.BinaryPath(t)
	// Discard the nested start's output: its log path length varies per run.
	script := fmt.Sprintf("%q job start --tenant team --environment dev --name gate --id gate -- sleep 30 >/dev/null 2>&1\nexit 0\n", bin)
	// Shrink the gate wait so the scenario reaches gate-incomplete quickly.
	envVars := append(jobStubEnv(t, setup, script),
		"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=100ms", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms")

	start := startJob(t, setup, envVars, "outer", "--", "work")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}
	t.Cleanup(func() {
		stopJob(t, setup, envVars, "gate")
	})

	status := waitForJobToLeaveRunning(t, setup, envVars, "outer")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 1 {
		t.Fatalf("expected a gate-incomplete job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
	}
	// exitCode 0 beside gate-incomplete must still report succeeded=false.
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
}

func testJobAJobThatWaitsForAJobItStartedReportsTheRealOutcomeOnceItFinishes(t *testing.T) {
	// A started job that finishes within the wait yields the real combined outcome.
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
}

func testJobAJobThatWaitsForAFailedJobItStartedSurfacesStartedJobFailed(t *testing.T) {
	// A started job's failure surfaces even though the parent exited 0.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	bin := erun.BinaryPath(t)
	stubs := filepath.Join(setup.Cwd, "stubs")
	// The scrubbed PATH has no sh, so the gate is a declared stub that really
	// fails rather than one that fails to start.
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
}

func testJobStartedJobFailedIgnoresASupersededEarlierAttemptUnderTheSameName(t *testing.T) {
	// A rerun under the same --name with a fresh --id supersedes the earlier
	// failed attempt, so the parent does not name the stale failure.
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
			stopJob(t, setup, envVars, id)
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
}

func testJobAJobThatEndsWhileABackgroundTaskJobItStartedIsStillRunningReadsAsGateIncomplete(t *testing.T) {
	// A running task job (seeded, since tasks run inside the MCP server) whose
	// startedByJobId names the parent makes the parent gate-incomplete. The pid is
	// this test's own, so it reads as alive.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	dir, logPath := seedTaskJobLog(t, setup, "task-child", "building...\n")
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

	// The task never finishes, so shrink the wait.
	envVars := append(jobStubEnv(t, setup, "exit 0"),
		"ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP=100ms", "ERUN_JOB_GATE_INCOMPLETE_POLL=20ms")
	start := startJob(t, setup, envVars, "outer", "--", "work")
	if start.ExitCode != 0 {
		t.Fatalf("start: exit %d: %s", start.ExitCode, start.Combined)
	}

	waitForJobToLeaveRunning(t, setup, envVars, "outer")
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

	assertTaskChildStartedByOuter(t, setup, envVars)
}

// seedTaskJobLog writes a task job's log and returns its job directory.
func seedTaskJobLog(t *testing.T, setup env.Setup, id, contents string) (dir, logPath string) {
	t.Helper()
	dir = filepath.Dir(jobRecordPath(setup, "team", "dev", id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	logPath = filepath.Join(dir, id+".log")
	if err := os.WriteFile(logPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("seed task log: %v", err)
	}
	return dir, logPath
}

func assertTaskChildStartedByOuter(t *testing.T, setup env.Setup, envVars []string) {
	t.Helper()
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
}

func testJobAJobThatWaitsForAFailedBackgroundTaskJobItStartedSurfacesStartedJobFailedAndLeavesItsLogReadable(t *testing.T) {
	// A task job that already failed surfaces as startedJobFailed, and its log is
	// readable via `job output`.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	dir, logPath := seedTaskJobLog(t, setup, "task-fail", "pushing image...\nauth failed\n")
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

	childStatus := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "task-fail"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if !strings.Contains(childStatus.Combined, "authentication required") {
		t.Fatalf("expected the task job's own status line to name its recorded reason, got: %s", childStatus.Combined)
	}
	output := erun.Run(t, []string{"job", "output", "--tenant", "team", "--environment", "dev", "--id", "task-fail"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if !strings.Contains(output.Stdout, "auth failed") {
		t.Fatalf("expected the task job's log to be readable via job output, got: %s", output.Combined)
	}
}

func testJobATaskJobStartedWithNoParentStaysParentless(t *testing.T) {
	// A task job with no startedByJobId reports no parent and does not block an
	// unrelated job running alongside it.
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
}

func testJobHandoffExcludesAJobFromItsParentsFinishCheck(t *testing.T) {
	// A --handoff job is excluded from the parent's finish check.
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
		stopJob(t, setup, envVars, "released")
	})

	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "10s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 0 {
		t.Fatalf("expected outer to succeed on its own despite the handed-off job still running, got %d:\n%s", await.ExitCode, await.Combined)
	}
	released := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "released"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if !strings.HasPrefix(released.Combined, "running:") {
		t.Fatalf("expected the handed-off job to still be running on its own after outer finished, got: %s", released.Combined)
	}
}
