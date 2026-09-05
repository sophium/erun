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
