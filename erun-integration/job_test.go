package integration

import (
	"encoding/json"
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
	return append(setup.Env(), fixture.StubEnv(stubs, "work")...)
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

func TestJob(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/help", normalize.Apply(result.Combined))
	})

	t.Run("await_help", func(t *testing.T) {
		// The await exit codes are the contract an orchestrator branches on, so the
		// help that states them is locked here.
		setup := env.New(t)
		result := erun.Run(t, []string{"job", "await", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/await_help", normalize.Apply(result.Combined))
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
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "stranded"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if status.ExitCode != 0 {
			t.Fatalf("status: exit %d: %s", status.ExitCode, status.Combined)
		}
		await := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "stranded", "--timeout", "1s"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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

	t.Run("attach_tracks_a_pid_without_claiming_an_outcome", func(t *testing.T) {
		// Work erun did not start still gets a handle and a lease. The pid seeded
		// here cannot exist, so the job resolves to unknown immediately — which is
		// the honest answer for work nothing erun ran was waiting on.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		attach := erun.Run(t, []string{"job", "attach", "--tenant", "team", "--environment", "dev", "--name", "overnight index", "--id", "overnight", "--pid", "2147483646"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if attach.ExitCode != 0 {
			t.Fatalf("attach: exit %d: %s", attach.ExitCode, attach.Combined)
		}
		status := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "overnight"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev", "--id", "nothing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an unknown job, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "job/status_of_an_unknown_id_names_the_environment", normalize.Apply(result.Combined))
	})

	t.Run("status_without_jobs_says_so", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"job", "status", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "job/status_without_jobs_says_so", normalize.Apply(result.Combined))
	})

	t.Run("start_requires_target_and_name", func(t *testing.T) {
		setup := env.New(t)
		missingTarget := erun.Run(t, []string{"job", "start", "--name", "suite", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if missingTarget.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without target flags, got 0:\n%s", missingTarget.Combined)
		}
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		missingName := erun.Run(t, []string{"job", "start", "--tenant", "team", "--environment", "dev", "--", "work"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"job", "await", "--tenant", "team", "--environment", "dev", "--id", "suite", "--timeout", "2h"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
}
