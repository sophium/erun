package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The banner that names an occupancy can only offer to stop it if it knows
// which job is behind the lease -- and must not offer that for a lease held by
// anything else.
func TestLeasesCarryTheirJobOnlyWhenAJobHoldsThem(t *testing.T) {
	now := time.Unix(1700000100, 0)
	out := environmentLeasesToUI([]eruncommon.EnvironmentActivityLease{
		{ID: "job-gate-1", Name: "repo gate", StartedAt: time.Unix(1700000000, 0)},
		{ID: "orchestrator-erun", Name: "erun", StartedAt: time.Unix(1700000000, 0)},
	}, now)

	if len(out) != 2 {
		t.Fatalf("expected both leases, got %d", len(out))
	}
	if out[0].JobID != "gate-1" {
		t.Fatalf("a job lease must name its job, got %q", out[0].JobID)
	}
	if out[1].JobID != "" {
		t.Fatalf("a non-job lease must name no job, got %q", out[1].JobID)
	}
	if out[0].SecondsHeld != 100 {
		t.Fatalf("held seconds: got %d", out[0].SecondsHeld)
	}
}

// TestVerifyLeaseJobIDsClearsAJobIDWithNoJobBehindIt is the regression test for
// a lease taken by hand with a name that happens to start with "job-" (the
// CLI's own --help example is literally `--name job-fix-1245`, and no --id)
// producing a lease id that is shape-identical to a real job's presence
// lease, so environmentLeasesToUI's id-shape match alone reported a JobID for
// it. The occupancy banner then rendered a "View jobs" button for a job that
// never existed, and the Jobs tab it linked to reported "No jobs yet".
// Without the fix this test fails because the hand-lease's candidate JobID
// survives unverified.
func TestVerifyLeaseJobIDsClearsAJobIDWithNoJobBehindIt(t *testing.T) {
	isolateJobsCache(t)
	const tenant = "erun"
	const environment = "ux"

	if _, err := eruncommon.AttachEnvironmentJob(eruncommon.Context{}, eruncommon.AttachEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          "gate-9",
		Name:        "repo gate",
		PID:         1,
	}); err != nil {
		t.Fatalf("attach real job: %v", err)
	}

	app := NewApp(erunUIDeps{
		store: jobsTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		canConnectLocalPort: func(int) bool {
			return false
		},
	})

	leases := []uiEnvironmentLease{
		{Name: "repo gate", JobID: "gate-9"},
		// Shape-identical candidate id ("visual-demo" strips cleanly off
		// "job-"), but no job record exists for it -- exactly the hand-taken
		// lease from the issue's reproduction.
		{Name: "job-visual-demo", JobID: "visual-demo"},
	}
	app.verifyLeaseJobIDs(tenant, environment, leases)

	if leases[0].JobID != "gate-9" {
		t.Fatalf("a lease backed by a real job must keep its job id, got %q", leases[0].JobID)
	}
	if leases[1].JobID != "" {
		t.Fatalf("a lease with no job record behind it must not offer to act on one, got %q", leases[1].JobID)
	}
}
