package activity

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// A claim taken over MCP must be the same claim the in-pod CLI's gate-merge
// guard refuses on, so these tests cross transports.

// inPodGuardIdentity sets the tenant/environment the runtime chart injects.
func inPodGuardIdentity(t *testing.T, tenant, environment string) {
	t.Helper()
	t.Setenv("ERUN_TENANT", tenant)
	t.Setenv("ERUN_ENVIRONMENT", environment)
}

func TestExclusiveClaimTakenOverMCPRefusesTheInPodGateMergeGuard(t *testing.T) {
	isolateLeaseCache(t)
	inPodGuardIdentity(t, "erun", "code1")
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "erun", Environment: "code1"}}

	// No explicit scope, so the claim lands at the exclusive default.
	_, taken, err := ActivityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "merge-queue drive 2442", ID: "merge-queue", Exclusive: true, Orchestrator: "erun",
	})
	if err != nil {
		t.Fatalf("take over MCP: %v", err)
	}
	if taken.Lease == nil || !taken.Lease.Exclusive || taken.Lease.Scope != "worktree" {
		t.Fatalf("expected an exclusive worktree-scoped claim, got %+v", taken.Lease)
	}

	expectInPodGuardRefusal(t, "merge-queue", "erun")
	expectInPodGuardExemption(t, "merge-queue")
}

func expectInPodGuardRefusal(t *testing.T, holderID, orchestrator string) {
	t.Helper()
	err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "")
	if err == nil {
		t.Fatal("a claim taken over MCP must refuse the in-pod gate-merge guard, got no error")
	}
	var conflict *jobexclusive.EnvironmentExclusivityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a refusal naming the holder, got %v", err)
	}
	if conflict.Holder.ID != holderID || conflict.Holder.Holder.Orchestrator != orchestrator {
		t.Fatalf("refusal must name the MCP-side holder, got %+v", conflict.Holder)
	}
}

func expectInPodGuardExemption(t *testing.T, underLeaseID string) {
	t.Helper()
	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", underLeaseID); err != nil {
		t.Fatalf("the claim's own holder must not be refused, got %v", err)
	}
	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "another-drive"); err == nil {
		t.Fatal("--under-lease must exempt only the caller's own claim")
	}
}

func TestExclusiveClaimTakenByTheInPodCLIIsReadBackOverMCPAsExclusive(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "erun", Environment: "code1"}}

	if _, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "code1", Name: "in-pod drive", ID: "in-pod-drive",
		Exclusive: true, Holder: model.EnvironmentActivityLeaseHolder{Orchestrator: "erun"},
	}); err != nil {
		t.Fatalf("take in-pod: %v", err)
	}

	_, listed, err := ActivityLeaseListTool(runtime)(context.Background(), nil, ActivityLeaseListInput{})
	if err != nil {
		t.Fatalf("list over MCP: %v", err)
	}
	if len(listed.Held) != 1 {
		t.Fatalf("expected the in-pod claim to be visible over MCP, got %+v", listed.Held)
	}
	held := listed.Held[0]
	if !held.Exclusive || held.Scope != "worktree" || held.Holder.Orchestrator != "erun" {
		t.Fatalf("expected the in-pod claim to read back as exclusive with its holder and scope, got %+v", held)
	}
}
