package jobs

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
)

// A job's plain presence lease must record its initiator, not an unnamed holder.
func TestJobPresenceLeaseRecordsInitiator(t *testing.T) {
	isolateActivityCache(t)
	t.Setenv("ERUN_ORCHESTRATOR_ID", "erun-devops")

	const tenant = "erun"
	const environment = "build"
	const id = "rel-erun-248"

	recorder, err := registerEnvironmentJob(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        "release erun 1.0.248",
		Command:     []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_, stop := startEnvironmentJobHeartbeat(tenant, environment, recorder, 15*time.Minute, nil, false)
	defer stop()

	leases, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		t.Fatalf("load leases: %v", err)
	}
	leaseID := environmentJobLeaseID(id)
	var found model.EnvironmentActivityLease
	var ok bool
	for _, lease := range leases {
		if lease.ID == leaseID {
			found, ok = lease, true
			break
		}
	}
	if !ok {
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
