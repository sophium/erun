package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A job's supervisor is the only writer of that job's outcome. Everything this
// test asserts follows from that: once the supervisor's process is gone, a
// record it left reading "running" can only be reconciled against that death,
// and every later read reports EnvironmentJobStateUnknown with no exit status
// and nothing naming what happened to the work. The supervisor itself does know
// why it is ending, so that has to be what a reader gets.

const jobSupervisorSetupFailureHelperEnv = "ERUN_COMMON_TEST_JOB_SUPERVISOR_SETUP_FAILURE_HELPER"

// runJobSupervisorSetupFailureHelper re-enters this test binary as a real
// supervisor process that is handed work it rejects at a point its own running
// record is already durable (a negative lease TTL, refused by
// normalizeEnvironmentJobLeaseTTL after registerEnvironmentJob has written the
// record), and then exits. That is the shape under test -- a supervisor process
// ending on a path that never reaches finishEnvironmentJob -- reached through
// the real entrypoint rather than a hand-seeded record, so the pid the record
// names is a process that genuinely ran and genuinely exited.
func runJobSupervisorSetupFailureHelper() int {
	id := os.Getenv(jobAliveSupervisorHelperIDEnv)
	err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      os.Getenv(jobAliveSupervisorHelperTenantEnv),
		Environment: os.Getenv(jobAliveSupervisorHelperEnvNameEnv),
		ID:          id,
		Name:        id,
		Command:     []string{"sleep", "60"},
		LeaseTTL:    -1 * time.Second,
	})
	if err == nil {
		fmt.Fprintln(os.Stderr, "the supervisor accepted a negative lease ttl")
		return 1
	}
	fmt.Fprintln(os.Stderr, err)
	return 0
}

// TestEnvironmentJobSupervisorRecordsWhyItsOwnProcessEnded is the reproduction
// of #2590's operator-visible state: after a supervisor's process is gone, the
// job must name an outcome rather than reading as unknown. Pre-fix the record
// the helper's process left behind still says "running", and the load below --
// the same one `erun job status` and `erun job await` go through -- answers with
// `unknown: job supervisor N is gone without recording an exit status`, no exit
// code and no reason.
func TestEnvironmentJobSupervisorRecordsWhyItsOwnProcessEnded(t *testing.T) {
	isolateActivityCache(t)
	const tenant = "supervisor-failure"
	const environment = "dev"
	const id = "unstartable"

	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(),
		jobSupervisorSetupFailureHelperEnv+"=1",
		jobAliveSupervisorHelperTenantEnv+"="+tenant,
		jobAliveSupervisorHelperEnvNameEnv+"="+environment,
		jobAliveSupervisorHelperIDEnv+"="+id,
	)
	// Waiting for exit is the point: the supervisor process must be gone before
	// the record is read, or the reconcile below would be asking the wrong
	// question.
	if out, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("supervisor helper: %v: %s", err, out)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.State == EnvironmentJobStateUnknown {
		t.Fatalf("the supervisor ended without recording an outcome: %s", job.Reason)
	}
	if job.State != EnvironmentJobStateExited {
		t.Fatalf("state = %q, want %q", job.State, EnvironmentJobStateExited)
	}
	if job.ExitCode == nil || *job.ExitCode == 0 {
		t.Fatalf("exit code = %v, want a recorded nonzero failure", job.ExitCode)
	}
	if !strings.Contains(job.Reason, "lease ttl") {
		t.Fatalf("reason = %q, want it to name the failure that ended the supervisor", job.Reason)
	}
}

// TestRecordEnvironmentJobSupervisorFailureLeavesADurableOutcomeAlone covers
// the guard the reproduction above cannot reach: a supervisor whose outcome is
// already written must not have it replaced by the failure handler, and a
// supervisor that ended for no reason it observed must not write one at all.
func TestRecordEnvironmentJobSupervisorFailureLeavesADurableOutcomeAlone(t *testing.T) {
	dir := t.TempDir()
	const id = "settled"
	code := 7
	recorder := &jobRecorder{dir: dir, job: EnvironmentJob{
		ID:       id,
		State:    EnvironmentJobStateExited,
		ExitCode: &code,
		Reason:   "the outcome finishEnvironmentJob already recorded",
	}}
	if err := writeEnvironmentJob(dir, recorder.job); err != nil {
		t.Fatalf("writeEnvironmentJob: %v", err)
	}

	recordEnvironmentJobSupervisorFailure(recorder, fmt.Errorf("too late"), nil)

	job, err := readEnvironmentJob(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("readEnvironmentJob: %v", err)
	}
	if job.ExitCode == nil || *job.ExitCode != code || job.Reason != recorder.job.Reason {
		t.Fatalf("settled outcome was overwritten: %+v", job)
	}

	// An unrecorded job that ended for a cause its supervisor did observe gets
	// a definite outcome, and the panic leg of the same handler is what a
	// supervisor crashing in its own finish path goes through.
	panicked := &jobRecorder{dir: dir, job: EnvironmentJob{ID: "crashed", State: EnvironmentJobStateRunning}}
	recordEnvironmentJobSupervisorFailure(panicked, nil, "nil map write")
	crashed := panicked.snapshot()
	if crashed.State != EnvironmentJobStateExited || crashed.ExitCode == nil || *crashed.ExitCode == 0 {
		t.Fatalf("panicked supervisor's record = %+v, want a recorded failure", crashed)
	}
	if !strings.Contains(crashed.Reason, "nil map write") {
		t.Fatalf("reason = %q, want it to name the panic", crashed.Reason)
	}
}
