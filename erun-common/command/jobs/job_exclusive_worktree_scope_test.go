package jobs

import (
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
)

// seedExclusiveClaim takes a claim through the store's own defaults, not a direct file write.
func seedExclusiveClaim(t *testing.T, tenant, environment, id, name, scope string) {
	t.Helper()
	if _, err := activitylease.TakeEnvironmentActivityLease(activitylease.TakeEnvironmentActivityLeaseParams{
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
	seedExclusiveClaim(t, "erun", "code1", "merge-queue", "merge-queue drive 2442", activitylease.DefaultEnvironmentActivityLeaseScope)
	seedExclusiveClaim(t, "erun", "code1", "other-gate", "another gate", activitylease.EnvironmentActivityLeaseScopeEnvironment)

	err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(core.Context{}, "gate-merge", "merge-queue")
	if err == nil {
		t.Fatal("another holder's environment-wide claim must still refuse this caller")
	}
	if !strings.Contains(err.Error(), "other-gate") {
		t.Errorf("expected the refusal to name the claim that actually refuses it, got %q", err.Error())
	}
}
