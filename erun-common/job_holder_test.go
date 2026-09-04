package eruncommon

import (
	"testing"
	"time"
)

// TestJobPresenceLeaseRecordsInitiator is the regression test for erun#2119.
// A job's own plain activity lease — the one every started job holds for its
// whole lifetime, not only the exclusive-scope claim an --exclusive job also
// takes — recorded no holder at all, so a refusal fed by it could only ever
// say "an unnamed holder" even though the orchestrator that started the job
// was known throughout (ERUN_ORCHESTRATOR_ID, read by environmentJobHolder).
func TestJobPresenceLeaseRecordsInitiator(t *testing.T) {
	isolateActivityCache(t)
	t.Setenv("ERUN_ORCHESTRATOR_ID", "erun-devops")

	const tenant = "erun"
	const environment = "build"
	const id = "rel-erun-248"

	recorder, err := registerEnvironmentJob(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        "release erun 1.0.248",
		Command:     []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// startEnvironmentJobHeartbeat takes the lease synchronously (refresh(true))
	// before it returns, so the lease is on disk the moment this call comes back.
	_, stop := startEnvironmentJobHeartbeat(tenant, environment, recorder, 15*time.Minute, nil, false)
	defer stop()

	leases, err := LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		t.Fatalf("load leases: %v", err)
	}
	leaseID := environmentJobLeaseID(id)
	var found *EnvironmentActivityLease
	for i := range leases {
		if leases[i].ID == leaseID {
			found = &leases[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("job's own presence lease %q not found among %+v", leaseID, leases)
	}
	if found.Holder.Orchestrator != "erun-devops" {
		t.Errorf("Holder.Orchestrator = %q, want %q", found.Holder.Orchestrator, "erun-devops")
	}
	if found.Holder.Tenant != tenant {
		t.Errorf("Holder.Tenant = %q, want %q", found.Holder.Tenant, tenant)
	}
	if got := found.Holder.String(); got == "an unnamed holder" {
		t.Errorf("a refusal fed by this lease would say %q instead of naming the job's initiator", got)
	}
}
