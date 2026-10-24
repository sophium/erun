package main

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity"
)

func TestLeasesCarryTheirJobOnlyWhenAJobHoldsThem(t *testing.T) {
	now := time.Unix(1700000100, 0)
	out := environmentLeasesToUI([]activity.EnvironmentActivityLease{
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

// The CLI's own --help example (`--name job-fix-1245`) yields a job-shaped id.
func TestVerifyLeaseJobIDsClearsAJobIDWithNoJobBehindIt(t *testing.T) {
	isolateJobsCache(t)
	const tenant = "erun"
	const environment = "ux"

	if _, err := jobs.AttachEnvironmentJob(core.Context{}, jobs.AttachEnvironmentJobParams{
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
