package eruncommon

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A test covering only "the first release claims it and nothing else races"
// would pass while the bug this claim exists for still ships: two
// orchestrators releasing the same version concurrently, discovered only by
// a tag collision at the end. These two cover the refusal and the reclaim.

func TestReleaseVersionClaimRefusesASecondReleaseAndNamesTheHolder(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)

	// Two distinct pids stand in for two distinct orchestrator processes
	// releasing the same version concurrently, as erun#1619 actually happened.
	firstPID, secondPID := os.Getpid(), os.Getpid()+1

	firstHolder := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a"}
	if _, err := takeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, firstPID, now); err != nil {
		t.Fatalf("first release's claim: %v", err)
	}

	secondHolder := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b"}
	_, err := takeReleaseVersionClaim("erun", "build", "1.0.211", secondHolder, secondPID, now.Add(time.Second))
	if err == nil {
		t.Fatal("expected a second concurrent release of the same version to be refused")
	}
	refusal := releaseVersionClaimRefusalError("1.0.211", err)
	if !strings.Contains(refusal.Error(), "1.0.211") {
		t.Errorf("refusal must name the version, got: %v", refusal)
	}
	if !strings.Contains(refusal.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the holder so a second orchestrator knows who to wait on, got: %v", refusal)
	}

	// A release of a different version in the same environment must not be
	// affected: the claim is version-scoped, not worktree-wide.
	if _, err := takeReleaseVersionClaim("erun", "build", "1.0.212", secondHolder, secondPID, now.Add(time.Second)); err != nil {
		t.Fatalf("a release of a different version must not collide with an unrelated one in flight: %v", err)
	}

	// The same process renewing its own claim (same pid, hence same id) must
	// keep succeeding rather than fighting its own lease.
	if _, err := takeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, firstPID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("the original holder renewing its own claim must succeed: %v", err)
	}
}

func TestReleaseVersionClaimReclaimsAnAbandonedRelease(t *testing.T) {
	isolateActivityCache(t)
	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)

	firstHolder := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a"}
	if _, err := takeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, os.Getpid(), start); err != nil {
		t.Fatalf("first release's claim: %v", err)
	}

	// The first holder crashed (or its pod was replaced) and stopped
	// renewing. Nobody can delete its claim by hand from outside that
	// environment, so the only way a second release ever proceeds is if the
	// lease reclaims itself once it lapses.
	afterLapse := start.Add(releaseVersionClaimTTL + time.Minute)
	secondHolder := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b"}
	claim, err := takeReleaseVersionClaim("erun", "build", "1.0.211", secondHolder, os.Getpid()+1, afterLapse)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}
	if claim.Holder.Orchestrator != "orchestrator-b" {
		t.Fatalf("expected the new release to hold the claim, got %+v", claim)
	}
}
