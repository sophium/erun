package erunmcp

import (
	"context"
	"errors"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// An exclusivity defect *between* the transports has to be asserted across
// both rather than within either: a claim an orchestrator took through this
// pod's MCP edge has to be the same claim the CLI running in that pod refuses
// gate-merge on. Taking over MCP and asserting only that MCP reads it back is
// exactly the check that passed while the guard stayed inert.

// inPodGuardIdentity makes the guard below behave as it does in the pod the
// MCP edge serves: ERUN_TENANT/ERUN_ENVIRONMENT are what the runtime chart
// injects, and are what scope the check to a real environment.
func inPodGuardIdentity(t *testing.T, tenant, environment string) {
	t.Helper()
	t.Setenv("ERUN_TENANT", tenant)
	t.Setenv("ERUN_ENVIRONMENT", environment)
}

func TestExclusiveClaimTakenOverMCPRefusesTheInPodGateMergeGuard(t *testing.T) {
	isolateLeaseCache(t)
	inPodGuardIdentity(t, "erun", "code1")
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "erun", Environment: "code1"}}

	// The shape reported: name, id, exclusive, and no explicit scope, so the
	// claim lands at the scope an exclusive take documents as its default.
	_, taken, err := activityLeaseTakeTool(runtime)(context.Background(), nil, ActivityLeaseTakeInput{
		Name: "merge-queue drive 2442", ID: "merge-queue", Exclusive: true, Orchestrator: "erun",
	})
	if err != nil {
		t.Fatalf("take over MCP: %v", err)
	}
	if taken.Lease == nil || !taken.Lease.Exclusive || taken.Lease.Scope != "worktree" {
		t.Fatalf("expected an exclusive worktree-scoped claim, got %+v", taken.Lease)
	}

	// The in-pod CLI, in the same store, with no --under-lease: it must refuse
	// rather than report there is no claim and rewrite the shared worktree.
	expectInPodGuardRefusal(t, "merge-queue", "erun")
	// The drive that took the claim names it and proceeds; anyone else does not.
	expectInPodGuardExemption(t, "merge-queue")
}

// expectInPodGuardRefusal runs the guard as the in-pod CLI does and asserts it
// refuses, naming the holder the MCP edge recorded.
func expectInPodGuardRefusal(t *testing.T, holderID, orchestrator string) {
	t.Helper()
	err := eruncommon.EnsureEnvironmentNotExclusivelyHeld(eruncommon.Context{}, "gate-merge", "")
	if err == nil {
		t.Fatal("a claim taken over MCP must refuse the in-pod gate-merge guard, got no error")
	}
	var conflict *eruncommon.EnvironmentExclusivityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a refusal naming the holder, got %v", err)
	}
	if conflict.Holder.ID != holderID || conflict.Holder.Holder.Orchestrator != orchestrator {
		t.Fatalf("refusal must name the MCP-side holder, got %+v", conflict.Holder)
	}
}

// expectInPodGuardExemption asserts --under-lease exempts the named claim and
// nothing else.
func expectInPodGuardExemption(t *testing.T, underLeaseID string) {
	t.Helper()
	if err := eruncommon.EnsureEnvironmentNotExclusivelyHeld(eruncommon.Context{}, "gate-merge", underLeaseID); err != nil {
		t.Fatalf("the claim's own holder must not be refused, got %v", err)
	}
	if err := eruncommon.EnsureEnvironmentNotExclusivelyHeld(eruncommon.Context{}, "gate-merge", "another-drive"); err == nil {
		t.Fatal("--under-lease must exempt only the caller's own claim")
	}
}

// The mirror direction: a claim the CLI took in the pod must be one the MCP
// edge reads back as exclusive, with its holder and scope. Both transports
// write the same store, and a test that only ever writes on one side is what
// let the two drift.
func TestExclusiveClaimTakenByTheInPodCLIIsReadBackOverMCPAsExclusive(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "erun", Environment: "code1"}}

	if _, err := eruncommon.TakeEnvironmentActivityLease(eruncommon.TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "code1", Name: "in-pod drive", ID: "in-pod-drive",
		Exclusive: true, Holder: eruncommon.EnvironmentActivityLeaseHolder{Orchestrator: "erun"},
	}); err != nil {
		t.Fatalf("take in-pod: %v", err)
	}

	_, listed, err := activityLeaseListTool(runtime)(context.Background(), nil, ActivityLeaseListInput{})
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
