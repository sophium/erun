package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

const (
	jobSupervisorStartupHelperEnv        = "ERUN_COMMON_TEST_JOB_SUPERVISOR_STARTUP_HELPER"
	jobSupervisorStartupModeSetupFailure = "setup-failure"
	// jobSupervisorStartupModeSilent stops without output, like a supervisor killed outright.
	jobSupervisorStartupModeSilent = "silent"

	testSupervisorStartupTenant      = "supervisor-startup"
	testSupervisorStartupEnvironment = "dev"
)

func init() {
	reentryHelpers = append(reentryHelpers, func() (int, bool) {
		if os.Getenv(jobSupervisorStartupHelperEnv) == "" {
			return 0, false
		}
		return runJobSupervisorStartupHelper(os.Getenv(jobSupervisorStartupHelperEnv)), true
	})
}

// runJobSupervisorStartupHelper is a real supervisor that ends before
// registering, writing any error to stderr as the erun binary does.
func runJobSupervisorStartupHelper(mode string) int {
	id := os.Getenv(jobAliveSupervisorHelperIDEnv)
	params := model.EnvironmentJobSupervisorParams{
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

func startAgainstADyingSupervisor(t *testing.T, id, mode string) (string, error) {
	t.Helper()
	t.Setenv(jobSupervisorStartupHelperEnv, mode)
	t.Setenv(jobAliveSupervisorHelperTenantEnv, testSupervisorStartupTenant)
	t.Setenv(jobAliveSupervisorHelperEnvNameEnv, testSupervisorStartupEnvironment)
	t.Setenv(jobAliveSupervisorHelperIDEnv, id)

	dir, err := jobexclusive.EnvironmentJobDir(testSupervisorStartupTenant, testSupervisorStartupEnvironment)
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

// A supervisor that dies before registering must have its cause in the start error.
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
	if _, readErr := jobstate.ReadEnvironmentJob(filepath.Join(dir, id+".json")); readErr == nil {
		t.Fatal("this supervisor registered a record; the case is meant to cover the path that never got that far")
	}
}

// A silent death gets no invented cause.
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
