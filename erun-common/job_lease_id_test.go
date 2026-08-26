package eruncommon

import "testing"

// A lease held for a job must be resolvable back to that job, and a lease held
// for anything else must not be mistaken for one.
func TestEnvironmentJobIDFromLeaseID(t *testing.T) {
	if id, ok := EnvironmentJobIDFromLeaseID(environmentJobLeaseID("gate-1")); !ok || id != "gate-1" {
		t.Fatalf("round trip: got %q ok=%v", id, ok)
	}
	for _, other := range []string{"", "orchestrator-erun", "job-", "deploy"} {
		if id, ok := EnvironmentJobIDFromLeaseID(other); ok {
			t.Fatalf("%q must not resolve to a job, got %q", other, id)
		}
	}
}
