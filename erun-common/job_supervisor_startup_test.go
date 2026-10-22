package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A supervisor that ends before it registers anything leaves no record behind,
// so the start call is the only surface that can say what happened -- and the
// only thing that knows why is the supervisor's own dying output. These two
// cases pin opposite halves of that: the cause is carried when the supervisor
// had one to give, and no cause is invented when it did not.

const (
	jobSupervisorStartupHelperEnv = "ERUN_COMMON_TEST_JOB_SUPERVISOR_STARTUP_HELPER"
	// jobSupervisorStartupModeSetupFailure makes the re-entered supervisor reject
	// its own work at a point before it has registered anything.
	jobSupervisorStartupModeSetupFailure = "setup-failure"
	// jobSupervisorStartupModeSilent makes it stop without writing a word, which
	// is the shape a supervisor killed outright leaves behind.
	jobSupervisorStartupModeSilent = "silent"

	testSupervisorStartupTenant      = "supervisor-startup"
	testSupervisorStartupEnvironment = "dev"
)

// runJobSupervisorStartupHelper re-enters this test binary as a real supervisor
// process that ends before registerEnvironmentJob has made anything durable.
//
// Both modes are pre-registration by construction: setup-failure is a returned
// setup error (registerEnvironmentJob refuses a negative output limit before it
// writes the record), and silent is the case with no error at all, a process
// that simply stops.
//
// The erun binary writes a returned supervisor error to its own stderr (main.go's
// run -> logger.Fatal), so this does the same. That output is the only account of
// this death anything downstream can find, which is what the cases below read.
func runJobSupervisorStartupHelper(mode string) int {
	id := os.Getenv(jobAliveSupervisorHelperIDEnv)
	params := EnvironmentJobSupervisorParams{
		Tenant:      os.Getenv(jobAliveSupervisorHelperTenantEnv),
		Environment: os.Getenv(jobAliveSupervisorHelperEnvNameEnv),
		ID:          id,
		Name:        id,
		Command:     []string{"sleep", "60"},
	}
	if mode == jobSupervisorStartupModeSilent {
		return 0
	}
	params.MaxOutputBytes = -1
	if err := RunEnvironmentJobSupervisor(params); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "the supervisor accepted a negative output limit")
	return 1
}

// startAgainstADyingSupervisor re-enters this test binary as the supervisor for
// one job, in the mode given, and returns the directory the start watched along
// with the failure it produced.
func startAgainstADyingSupervisor(t *testing.T, id, mode string) (string, error) {
	t.Helper()
	t.Setenv(jobSupervisorStartupHelperEnv, mode)
	t.Setenv(jobAliveSupervisorHelperTenantEnv, testSupervisorStartupTenant)
	t.Setenv(jobAliveSupervisorHelperEnvNameEnv, testSupervisorStartupEnvironment)
	t.Setenv(jobAliveSupervisorHelperIDEnv, id)

	dir, err := environmentJobDir(testSupervisorStartupTenant, testSupervisorStartupEnvironment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	_, startErr := spawnEnvironmentJobSupervisor(dir, StartEnvironmentJobParams{
		Tenant:         testSupervisorStartupTenant,
		Environment:    testSupervisorStartupEnvironment,
		Name:           id,
		ID:             id,
		SupervisorPath: os.Args[0],
	}, nil)
	return dir, startErr
}

// TestEnvironmentJobStartNamesWhyASupervisorDiedBeforeRegistering is the
// reproduction of the operator-visible state this guards: a supervisor that ends
// before registering leaves no record, so the start call is all the operator
// gets, and pre-fix it reported only that the supervisor was gone --
//
//	job supervisor N exited without registering job "X"
//
// -- because the detached supervisor's stdout and stderr were nil, sending
// whatever killed it to /dev/null. The cause is what makes this failure
// actionable, and it is recoverable only from the supervisor's own output.
func TestEnvironmentJobStartNamesWhyASupervisorDiedBeforeRegistering(t *testing.T) {
	isolateActivityCache(t)
	const id = "died-before-registering"

	dir, err := startAgainstADyingSupervisor(t, id, jobSupervisorStartupModeSetupFailure)
	if err == nil {
		t.Fatal("a supervisor that died before registering produced no start failure")
	}
	if !strings.Contains(err.Error(), "exited without registering") {
		t.Fatalf("error = %q, want it to still report the start itself failing", err)
	}
	if !strings.Contains(err.Error(), "max output bytes must not be negative") {
		t.Fatalf("error = %q, want it to name the setup failure that ended the supervisor", err)
	}
	// Pre-registration is the scope these cases cover, and it is the reason the
	// start error has to carry the cause at all: with no record written, nothing
	// `job status` or `job await` reads can supply it later.
	if _, readErr := readEnvironmentJob(filepath.Join(dir, id+".json")); readErr == nil {
		t.Fatal("this supervisor registered a record; the case is meant to cover the path that never got that far")
	}
}

// TestEnvironmentJobStartInventsNoCauseForASupervisorThatSaidNothing covers the
// other half of the same path. A supervisor killed outright never gets to say
// why, and the bare "exited without registering" is then the whole truth:
// reporting a cause it did not give -- or quoting an earlier run's log back as
// this death's -- would be worse than reporting none.
func TestEnvironmentJobStartInventsNoCauseForASupervisorThatSaidNothing(t *testing.T) {
	isolateActivityCache(t)
	const id = "vanished-quietly"

	dir, err := startAgainstADyingSupervisor(t, id, jobSupervisorStartupModeSilent)
	if err == nil {
		t.Fatal("a supervisor that vanished before registering produced no start failure")
	}
	if want := fmt.Sprintf("exited without registering job %q", id); !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("error = %q, want it to end at %q with no cause appended", err, want)
	}
	data, readErr := os.ReadFile(environmentJobLogPath(dir, id))
	if readErr != nil {
		t.Fatalf("readEnvironmentJobLog: %v", readErr)
	}
	if text := strings.TrimSpace(string(data)); text != "" {
		t.Fatalf("the supervisor was expected to say nothing, but its log holds %q", text)
	}
}
