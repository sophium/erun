package integration

import (
	"encoding/json"
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

func testJobAwaitReportsATimeoutDistinctlyFromAFailure(t *testing.T) {
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
}

func testJobStatusReportsANonZeroExitCapturedInTheEnvironment(t *testing.T) {
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
}

func testJobStatusNamesWhyAFailedJobFailedInsteadOfOnlyItsExitCode(t *testing.T) {
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
}

func testJobOutputIsReadableWhileTheJobRuns(t *testing.T) {
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
}

func testJobARunningJobHoldsAnActivityLease(t *testing.T) {
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
	released := waitForJobActivityLeaseRelease(t, setup.Cwd, envVars)
	golden.Equal(t, "job/a_running_job_holds_an_activity_lease", normalize.Apply(held.Combined+released.Combined))
}

func testJobCancelSignalsTheRecordedProcessAndRecordsTheOutcome(t *testing.T) {
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
}

func testJobAttachTracksAPidWithoutClaimingAnOutcome(t *testing.T) {
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
}

func testJobStatusOfAnUnknownIdNamesTheEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "nothing"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unknown job, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "job/status_of_an_unknown_id_names_the_environment", normalize.Apply(result.Combined))
}

func testJobStatusWithoutJobsSaysSo(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "job/status_without_jobs_says_so", normalize.Apply(result.Combined))
}

func testJobStatusSkipsACorruptedRecordAndOrdersTiesById(t *testing.T) {
	// Listing every job tolerates an unreadable record instead of failing
	// the whole list: a record this process cannot even parse cannot
	// answer anything, and leaving it on disk would keep an unreadable job
	// in every listing forever, so it is removed as the list is built.
	// Two records sharing the same startedAt (two jobs started in the same
	// wall-clock second, a real possibility) also proves the list's
	// tie-break falls back to ID rather than whatever order the directory
	// happened to return. Both are seeded running (this test binary's own,
	// real, alive pid, the same technique the task-job scenarios above
	// use) rather than exited, so retention never prunes them regardless of
	// how old the fixed startedAt is.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	dir := filepath.Dir(jobRecordPath(setup, "team", "dev", "a"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, id := range []string{"b", "a"} {
		record := fmt.Sprintf(`{
  "id": %q,
  "name": %q,
  "state": "running",
  "kind": "command",
  "pid": %d,
  "startedAt": "2026-01-01T00:00:00Z",
  "leaseId": "job-%s"
}
`, id, id, os.Getpid(), id)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(record), 0o644); err != nil {
			t.Fatalf("seed job record %s: %v", id, err)
		}
	}
	corrupted := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(corrupted, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupted record: %v", err)
	}

	result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertJobsListedAThenB(t, result.Stdout)
	if strings.Contains(result.Stdout, "broken") {
		t.Errorf("expected the corrupted record to be skipped rather than listed, got:\n%s", result.Stdout)
	}
	if _, err := os.Stat(corrupted); !os.IsNotExist(err) {
		t.Errorf("expected the corrupted record to be removed from disk, stat err: %v", err)
	}
}

func assertJobsListedAThenB(t *testing.T, stdout string) {
	t.Helper()
	lastIndex := -1
	for _, id := range []string{"a", "b"} {
		index := strings.Index(stdout, "  "+id+":")
		if index < 0 {
			t.Fatalf("expected job %q in the list, got:\n%s", id, stdout)
		}
		if index < lastIndex {
			t.Fatalf("expected jobs ordered a, b (ties broken by id), got:\n%s", stdout)
		}
		lastIndex = index
	}
}

func testJobAwaitRefusesAnUnboundedTimeout(t *testing.T) {
	// The ceiling is what makes "bounded" real: a caller that wants to wait
	// longer polls again rather than parking on one call.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "2h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a timeout past the ceiling, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "job/await_refuses_an_unbounded_timeout", normalize.Apply(result.Combined))
}

func testJobCancelDryRunNamesTheTargetWithoutSignalling(t *testing.T) {
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
}

func testJobOutputPastTheCapIsReportedAsTruncated(t *testing.T) {
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
}
