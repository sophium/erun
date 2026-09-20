package eruncommon

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// An exclusive claim taken at the scope an exclusive take actually defaults to
// -- "worktree", the environment's one shared worktree, which is what
// `exec gate-merge` rewrites -- must refuse gate-merge. Reading only the
// "environment" scope made the guard inert for the claim shape every transport
// records when the caller does not name a scope, and it fails *open*: the
// refusal never fires and the drive rewrites the shared worktree anyway.

// seedExclusiveClaim takes an exclusive claim the way a transport does when its
// caller names no scope: through the store's own defaults, never by writing a
// file directly, so the test cannot agree with a bug the take path shares.
func seedExclusiveClaim(t *testing.T, tenant, environment, id, name, scope string) {
	t.Helper()
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: tenant, Environment: environment, Name: name, ID: id,
		Exclusive: true, Scope: scope, Now: time.Now(),
		Holder: EnvironmentActivityLeaseHolder{Orchestrator: "erun"},
	}); err != nil {
		t.Fatalf("take exclusive claim %s: %v", id, err)
	}
}

func inPodGuardEnv(t *testing.T, tenant, environment string) {
	t.Helper()
	isolateActivityCache(t)
	t.Setenv("ERUN_TENANT", tenant)
	t.Setenv("ERUN_ENVIRONMENT", environment)
}

func TestGateMergeGuardRefusesADefaultScopedExclusiveClaim(t *testing.T) {
	inPodGuardEnv(t, "erun", "code1")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", "")

	err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", "")
	if err == nil {
		t.Fatal("an exclusive claim at the default scope must refuse gate-merge, got no error")
	}
	var conflict *EnvironmentExclusivityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a structured refusal naming the holder, got %v", err)
	}
	if conflict.Holder.ID != "merge-queue" || conflict.Holder.Holder.Orchestrator != "erun" {
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
	// The remedy has to name the scope the claim was actually taken on, or the
	// release it suggests silently targets the other store and does nothing.
	if conflict.Scope != defaultEnvironmentActivityLeaseScope {
		t.Errorf("expected the refusal to carry the holder's own scope %q, got %q", defaultEnvironmentActivityLeaseScope, conflict.Scope)
	}
	if !strings.Contains(err.Error(), "--scope "+defaultEnvironmentActivityLeaseScope) {
		t.Errorf("expected the release remedy to name scope %q, got %q", defaultEnvironmentActivityLeaseScope, err.Error())
	}
}

func TestGateMergeGuardExemptsOnlyTheCallersOwnDefaultScopedClaim(t *testing.T) {
	inPodGuardEnv(t, "erun", "code1")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", "")

	if err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", "merge-queue"); err != nil {
		t.Fatalf("the claim's own holder must not be refused, got %v", err)
	}
	if err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", "some-other-drive"); err == nil {
		t.Fatal("--under-lease must exempt only the caller's own claim, not any claim")
	}
}

// A claim on a named resource that is not this environment's worktree -- the
// second clone in the same pod an exclusive take explicitly allows -- must not
// refuse gate-merge here, or the two-clones case scopes exist for stops
// working.
func TestGateMergeGuardIgnoresAClaimOnAnUnrelatedNamedScope(t *testing.T) {
	inPodGuardEnv(t, "erun", "pod")
	seedExclusiveClaim(t, "erun", "pod", "clone-b", "clone b build", "/git/clone-b")

	if err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", ""); err != nil {
		t.Fatalf("a claim on another clone's scope must not refuse this worktree, got %v", err)
	}
}

// Two scopes can be held at once, one per scope. A caller that holds the
// worktree claim is still not entitled to work under someone else's
// environment-wide claim -- which is the stronger of the two, and the one the
// job-start guard already reads.
func TestGateMergeGuardRefusesADifferentHoldersEnvironmentClaimUnderTheCallersOwnWorktreeClaim(t *testing.T) {
	inPodGuardEnv(t, "erun", "code1")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", defaultEnvironmentActivityLeaseScope)
	seedExclusiveClaim(t, "erun", "code1", "other-gate", "another gate", EnvironmentActivityLeaseScopeEnvironment)

	err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", "merge-queue")
	if err == nil {
		t.Fatal("another holder's environment-wide claim must still refuse this caller")
	}
	if !strings.Contains(err.Error(), "other-gate") {
		t.Errorf("expected the refusal to name the claim that actually refuses it, got %q", err.Error())
	}
}

// Off-pod there is no environment to contend for, so the guard stays a no-op.
func TestGateMergeGuardIsANoOpOutsideAnEnvironment(t *testing.T) {
	isolateActivityCache(t)
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", "")

	if err := EnsureEnvironmentNotExclusivelyHeld(Context{}, "gate-merge", ""); err != nil {
		t.Fatalf("expected an off-pod no-op, got %v", err)
	}
}
