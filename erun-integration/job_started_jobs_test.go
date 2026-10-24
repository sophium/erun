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
	// The reproduction this closes: an agent job runs a gate through its own
	// `job start` (detach and await, from inside an agent's Bash tool) and
	// then ends -- by backgrounding itself,
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
		stopJob(t, setup, envVars, "gate")
	})

	status := waitForJobToLeaveRunning(t, setup, envVars, "outer")
	await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "outer", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if await.ExitCode != 1 {
		t.Fatalf("expected a gate-incomplete job to report a failure outcome, got %d:\n%s", await.ExitCode, await.Combined)
	}
	// : this is the exact shape the issue reported -- exitCode 0
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
}

func testJobAJobThatWaitsForAJobItStartedReportsTheRealOutcomeOnceItFinishes(t *testing.T) {
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
}

func testJobAJobThatWaitsForAFailedJobItStartedSurfacesStartedJobFailed(t *testing.T) {
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
}

func testJobStartedJobFailedIgnoresASupersededEarlierAttemptUnderTheSameName(t *testing.T) {
	// A caller that folds the tree and command into the nested job's --id
	// means an agent that fixes what a gate found and reruns it gets a fresh
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

// seedTaskJobLog writes the log a task job id was handed and returns the job
// directory beside it.
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
	// The other half of the fix: a task job that already failed before the
	// parent's own process even exits is picked up by the same
	// environmentJobFailedChildren scan a nested command job's failure is.
	// A task job is a Go call with no subprocess stdio to capture, so what a
	// poll can read back is only whatever the work itself wrote to the log
	// it was handed -- before this PR a failed task left nothing at all to
	// read, just a bare exit code and a bare error string.
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
}

func testJobATaskJobStartedWithNoParentStaysParentless(t *testing.T) {
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
}

func testJobHandoffExcludesAJobFromItsParentsFinishCheck(t *testing.T) {
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
