package integration

import (
	"encoding/json"
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

func testActivityLeaseHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "lease", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/lease_help", normalize.Apply(result.Combined))
}

func testActivityLeaseTakeHelpNamesWhatAnExclusiveClaimActuallyRefuses(t *testing.T) {
	// A job start consults only the "environment" scope, so help must not
	// claim a worktree claim refuses a second job.
	setup := env.New(t)
	result := erun.Run(t, []string{"activity", "lease", "take", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "already working the same worktree is refused") {
		t.Errorf("help must not claim a worktree-scoped claim refuses a second agent job, which only an environment-scoped claim does; got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "environment") {
		t.Errorf("help must name the scope a job start actually consults; got:\n%s", result.Combined)
	}
}

func testActivityLeaseTakeListAndRelease(t *testing.T) {
	// The remaining seconds are wall-clock dependent and normalized away.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "gradle-build", "--ttl", "10m"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	list := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	release := erun.Run(t, []string{"activity", "lease", "release", "--tenant", "team", "--environment", "dev", "--id", "gradle-build"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	empty := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	golden.Equal(t, "activity/lease_take_list_and_release", normalize.Apply(
		take.Combined+list.Combined+release.Combined+empty.Combined))

	body, err := os.ReadFile(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "leases", "gradle-build.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected the released lease file removed, got err %v body %s", err, body)
	}
}

func testActivityLeaseTakeJSONRecordsTheHolderProcess(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent run", "--id", "agent-run", "--pid", "4242", "--json"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	var lease struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		PID  int    `json:"pid"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &lease); err != nil {
		t.Fatalf("parse lease JSON: %v\n%s", err, result.Stdout)
	}
	if lease.ID != "agent-run" || lease.Name != "agent run" || lease.PID != 4242 {
		t.Errorf("unexpected lease %+v", lease)
	}
}

func testActivityLeaseTakeRequiresAName(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a name, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/lease_take_requires_a_name", normalize.Apply(result.Combined))
}

func testActivityLeaseTakeExclusiveDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1245", "--exclusive", "--orchestrator", "petios", "--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/lease_take_exclusive_dry_run", normalize.Apply(result.Combined))
}

func testActivityLeaseTakeExclusiveRefusesASecondHolderAndNamesIt(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	first := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1201", "--id", "job-fix-1201", "--exclusive", "--orchestrator", "petios",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if first.ExitCode != 0 {
		t.Fatalf("first take: exit %d: %s", first.ExitCode, first.Combined)
	}
	second := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1245", "--id", "job-fix-1245", "--exclusive", "--orchestrator", "erun",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if second.ExitCode == 0 {
		t.Fatalf("expected the second exclusive take to be refused, got exit 0: %s", second.Combined)
	}
	if !strings.Contains(second.Combined, "job-fix-1201") || !strings.Contains(second.Combined, "petios") {
		t.Fatalf("refusal must name the actual holder (id and orchestrator), got:\n%s", second.Combined)
	}
}

func testActivityLeaseTakeExclusiveAllowsADifferentScopeToCoexist(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	first := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "clone-a-job", "--id", "clone-a-job", "--exclusive", "--scope", "/git/clone-a",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if first.ExitCode != 0 {
		t.Fatalf("first take: exit %d: %s", first.ExitCode, first.Combined)
	}
	second := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "clone-b-job", "--id", "clone-b-job", "--exclusive", "--scope", "/git/clone-b",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if second.ExitCode != 0 {
		t.Fatalf("expected a different scope to succeed without conflict, got exit %d: %s", second.ExitCode, second.Combined)
	}
}

func testActivityLeaseReleaseExclusiveRoundTrip(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1245", "--id", "job-fix-1245", "--exclusive",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	release := erun.Run(t, []string{
		"activity", "lease", "release", "--tenant", "team", "--environment", "dev",
		"--id", "job-fix-1245", "--exclusive",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if release.ExitCode != 0 {
		t.Fatalf("release: exit %d: %s", release.ExitCode, release.Combined)
	}
	again := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1250", "--id", "job-fix-1250", "--exclusive",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if again.ExitCode != 0 {
		t.Fatalf("expected the scope free after release, got exit %d: %s", again.ExitCode, again.Combined)
	}
}

func testActivityLeaseReleaseReportsNotHeldForAnUnknownID(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"activity", "lease", "release", "--tenant", "team", "--environment", "dev", "--id", "never-taken",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode != 0 {
		t.Fatalf("expected releasing an unknown id to still succeed, got exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "activity/lease_release_reports_not_held_for_an_unknown_id", normalize.Apply(result.Combined))
}

func testActivityLeaseReleaseExclusiveWrongScopeReportsNotHeld(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-2414", "--id", "job-fix-2414", "--exclusive", "--scope", "/git/worktree-a",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	release := erun.Run(t, []string{
		"activity", "lease", "release", "--tenant", "team", "--environment", "dev",
		"--id", "job-fix-2414", "--exclusive", "--scope", "/git/worktree-b",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if release.ExitCode != 0 {
		t.Fatalf("expected releasing the wrong scope to still succeed as a no-op, got exit %d: %s", release.ExitCode, release.Combined)
	}
	golden.Equal(t, "activity/lease_release_exclusive_wrong_scope_reports_not_held", normalize.Apply(release.Combined))
	list := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if !strings.Contains(list.Combined, "job-fix-2414") {
		t.Fatalf("expected the real claim to survive a release aimed at the wrong scope, got:\n%s", list.Combined)
	}
}

func testActivityLeaseReleaseExclusiveHeldByAnotherIDNamesTheHolder(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "job-fix-1201", "--id", "job-fix-1201", "--exclusive", "--orchestrator", "petios",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	release := erun.Run(t, []string{
		"activity", "lease", "release", "--tenant", "team", "--environment", "dev",
		"--id", "somebody-else", "--exclusive",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if release.ExitCode == 0 {
		t.Fatalf("expected a release by the wrong id to fail, got exit 0: %s", release.Combined)
	}
	if !strings.Contains(release.Combined, "job-fix-1201") || !strings.Contains(release.Combined, "petios") {
		t.Fatalf("refusal must name the actual holder (id and orchestrator), got:\n%s", release.Combined)
	}
	list := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if !strings.Contains(list.Combined, "job-fix-1201") {
		t.Fatalf("expected the real holder's claim to survive a release by the wrong id, got:\n%s", list.Combined)
	}
}

func testActivityStopReadyBlockedByAHeldLease(t *testing.T) {
	// The window is pinned to a UTC day to stay on the in-hours arm.
	setup := env.New(t)
	seedManagedCloudTenantEnvWithIdle(t, setup, "team", "dev", insideWorkingHoursIdleBlock)
	take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent-run", "--ttl", "1h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a leased env to refuse the stop, got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_blocked_by_a_held_lease", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json")); !os.IsNotExist(err) {
		t.Errorf("a leased env must not arm a grace window, stat err: %v", err)
	}
}

func testActivityStopReadyBlockedByAHeldLeaseOutsideWorkingHours(t *testing.T) {
	// A held lease blocks stopping even outside working hours.
	setup := env.New(t)
	seedManagedCloudTenantEnvWithIdle(t, setup, "team", "dev", outsideWorkingHoursIdleBlock)
	take := erun.Run(t, []string{"activity", "lease", "take", "--tenant", "team", "--environment", "dev", "--name", "agent-run", "--ttl", "1h"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	result := erun.Run(t, []string{"activity", "stop-ready", "--tenant", "team", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if result.ExitCode == 0 {
		t.Fatalf("expected a leased env to refuse the stop outside working hours, got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "activity/stop_ready_blocked_by_a_held_lease_outside_working_hours", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(setup.Home, ".erun", "team", "dev", "stop-pending.json")); !os.IsNotExist(err) {
		t.Errorf("a leased env must not arm a grace window, stat err: %v", err)
	}
}
