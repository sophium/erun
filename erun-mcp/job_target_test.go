package erunmcp

import (
	"context"
	"strings"
	"testing"
)

// Every job, lease and idle tool takes a tenant/environment, and none of them can
// honour one that names a different environment: the work runs in this pod,
// against this pod's repo and this pod's erun binary. Before this was enforced,
// job_start accepted a foreign target, echoed it back in its result, and ran here
// anyway -- so the caller was handed a handle asserting a target the work never
// reached (#1195). Each of these must refuse.
func TestJobLeaseAndIdleToolsRefuseAForeignEnvironment(t *testing.T) {
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	// Assert on the REFUSAL, not merely on "an error". Several of these tools fail
	// for unrelated downstream reasons when handed a foreign target -- a missing
	// job, an unresolvable repo -- so a bare err != nil passes even against the
	// unfixed code and proves nothing. The error has to be the one that names both
	// scopes.
	_, _, err := jobStatusTool(runtime)(ctx, nil, JobStatusInput{Tenant: "tenant-b", Environment: "prod", ID: "x"})
	assertRefusedForeignTarget(t, "job_status", err)

	_, _, err = jobAwaitTool(runtime)(ctx, nil, JobAwaitInput{Tenant: "tenant-b", Environment: "prod", ID: "x"})
	assertRefusedForeignTarget(t, "job_await", err)

	_, _, err = activityLeaseTakeTool(runtime)(ctx, nil, ActivityLeaseTakeInput{Tenant: "tenant-b", Environment: "prod", Name: "x"})
	assertRefusedForeignTarget(t, "activity_lease_take", err)

	_, _, err = idleStopHistoryTool(runtime)(ctx, nil, IdleStopHistoryInput{Tenant: "tenant-b", Environment: "prod"})
	assertRefusedForeignTarget(t, "idle_stop_history", err)
}

// assertRefusedForeignTarget insists the tool refused BECAUSE the target is not
// this server's, naming the server's scope and the requested one. Any other
// error -- including a plausible downstream one -- is a failure, because it means
// the tool went on to act rather than refusing up front.
func assertRefusedForeignTarget(t *testing.T, tool string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s accepted a foreign environment instead of refusing it", tool)
		return
	}
	if !strings.Contains(err.Error(), "tenant-a/dev") || !strings.Contains(err.Error(), "tenant-b/prod") {
		t.Errorf("%s failed for the wrong reason -- want a refusal naming tenant-a/dev and tenant-b/prod, got: %v", tool, err)
	}
}

// Restating the server's own scope, or omitting it, must keep working -- the
// arguments are how a caller disambiguates, and refusing those would break every
// existing caller.
func TestJobToolsAcceptTheirOwnScope(t *testing.T) {
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	if _, _, err := jobStatusTool(runtime)(ctx, nil, JobStatusInput{Tenant: "tenant-a", Environment: "dev"}); err != nil {
		t.Errorf("job_status refused its own scope: %v", err)
	}
	if _, _, err := jobStatusTool(runtime)(ctx, nil, JobStatusInput{}); err != nil {
		t.Errorf("job_status refused an omitted scope: %v", err)
	}
}
