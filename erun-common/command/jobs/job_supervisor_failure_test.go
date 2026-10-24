package jobs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

const jobSupervisorSetupFailureHelperEnv = "ERUN_COMMON_TEST_JOB_SUPERVISOR_SETUP_FAILURE_HELPER"

func init() {
	reentryHelpers = append(reentryHelpers, func() (int, bool) {
		if os.Getenv(jobSupervisorSetupFailureHelperEnv) == "" {
			return 0, false
		}
		return runJobSupervisorSetupFailureHelper(), true
	})
}

// runJobSupervisorSetupFailureHelper is a real supervisor that fails setup after
// its running record is durable, then exits.
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

// A supervisor that dies after registering must leave an outcome, not "unknown".
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
	// The supervisor must be gone before the record is read.
	if out, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("supervisor helper: %v: %s", err, out)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.State == shared.EnvironmentJobStateUnknown {
		t.Fatalf("the supervisor ended without recording an outcome: %s", job.Reason)
	}
	if job.State != shared.EnvironmentJobStateExited {
		t.Fatalf("state = %q, want %q", job.State, shared.EnvironmentJobStateExited)
	}
	if job.ExitCode == nil || *job.ExitCode == 0 {
		t.Fatalf("exit code = %v, want a recorded nonzero failure", job.ExitCode)
	}
	if !strings.Contains(job.Reason, "lease ttl") {
		t.Fatalf("reason = %q, want it to name the failure that ended the supervisor", job.Reason)
	}
}

// The failure handler must not replace a durable outcome.
func TestRecordEnvironmentJobSupervisorFailureLeavesADurableOutcomeAlone(t *testing.T) {
	dir := t.TempDir()
	const id = "settled"
	code := 7
	recorder := &jobRecorder{dir: dir, job: shared.EnvironmentJob{
		ID:       id,
		State:    shared.EnvironmentJobStateExited,
		ExitCode: &code,
		Reason:   "the outcome finishEnvironmentJob already recorded",
	}}
	if err := writeEnvironmentJob(dir, recorder.job); err != nil {
		t.Fatalf("writeEnvironmentJob: %v", err)
	}

	recordEnvironmentJobSupervisorFailure(recorder, fmt.Errorf("too late"), nil)

	job, err := shared.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("readEnvironmentJob: %v", err)
	}
	if job.ExitCode == nil || *job.ExitCode != code || job.Reason != recorder.job.Reason {
		t.Fatalf("settled outcome was overwritten: %+v", job)
	}

	panicked := &jobRecorder{dir: dir, job: shared.EnvironmentJob{ID: "crashed", State: EnvironmentJobStateRunning}}
	recordEnvironmentJobSupervisorFailure(panicked, nil, "nil map write")
	crashed := panicked.snapshot()
	if crashed.State != shared.EnvironmentJobStateExited || crashed.ExitCode == nil || *crashed.ExitCode == 0 {
		t.Fatalf("panicked supervisor's record = %+v, want a recorded failure", crashed)
	}
	if !strings.Contains(crashed.Reason, "nil map write") {
		t.Fatalf("reason = %q, want it to name the panic", crashed.Reason)
	}
}
