package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
)

// The default exclusive scope is "worktree", which gate-merge rewrites, so it must refuse.

// seedExclusiveClaim takes a claim through the store's own defaults, not a direct file write.
func seedExclusiveClaim(t *testing.T, tenant, environment, id, name, scope string) {
	t.Helper()
	if _, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant: tenant, Environment: environment, Name: name, ID: id,
		Exclusive: true, Scope: scope, Now: time.Now(),
		Holder: model.EnvironmentActivityLeaseHolder{Orchestrator: "erun"},
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

	err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "")
	if err == nil {
		t.Fatal("an exclusive claim at the default scope must refuse gate-merge, got no error")
	}
	var conflict *jobexclusive.EnvironmentExclusivityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a structured refusal naming the holder, got %v", err)
	}
	if conflict.Holder.ID != "merge-queue" || conflict.Holder.Holder.Orchestrator != "erun" {
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
	if conflict.Scope != activity.DefaultEnvironmentActivityLeaseScope {
		t.Errorf("expected the refusal to carry the holder's own scope %q, got %q", activity.DefaultEnvironmentActivityLeaseScope, conflict.Scope)
	}
	if !strings.Contains(err.Error(), "--scope "+activity.DefaultEnvironmentActivityLeaseScope) {
		t.Errorf("expected the release remedy to name scope %q, got %q", activity.DefaultEnvironmentActivityLeaseScope, err.Error())
	}
}

func TestGateMergeGuardExemptsOnlyTheCallersOwnDefaultScopedClaim(t *testing.T) {
	inPodGuardEnv(t, "erun", "code1")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", "")

	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "merge-queue"); err != nil {
		t.Fatalf("the claim's own holder must not be refused, got %v", err)
	}
	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "some-other-drive"); err == nil {
		t.Fatal("--under-lease must exempt only the caller's own claim, not any claim")
	}
}

// Another clone's named scope must not refuse this worktree.
func TestGateMergeGuardIgnoresAClaimOnAnUnrelatedNamedScope(t *testing.T) {
	inPodGuardEnv(t, "erun", "pod")
	seedExclusiveClaim(t, "erun", "pod", "clone-b", "clone b build", "/git/clone-b")

	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", ""); err != nil {
		t.Fatalf("a claim on another clone's scope must not refuse this worktree, got %v", err)
	}
}

// Holding the worktree claim does not entitle working under another holder's environment claim.
func TestGateMergeGuardRefusesADifferentHoldersEnvironmentClaimUnderTheCallersOwnWorktreeClaim(t *testing.T) {
	inPodGuardEnv(t, "erun", "code1")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", activity.DefaultEnvironmentActivityLeaseScope)
	seedExclusiveClaim(t, "erun", "code1", "other-gate", "another gate", activity.EnvironmentActivityLeaseScopeEnvironment)

	err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "merge-queue")
	if err == nil {
		t.Fatal("another holder's environment-wide claim must still refuse this caller")
	}
	if !strings.Contains(err.Error(), "other-gate") {
		t.Errorf("expected the refusal to name the claim that actually refuses it, got %q", err.Error())
	}
}

func TestGateMergeGuardIsANoOpOutsideAnEnvironment(t *testing.T) {
	isolateActivityCache(t)
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", "")

	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", ""); err != nil {
		t.Fatalf("expected an off-pod no-op, got %v", err)
	}
}
