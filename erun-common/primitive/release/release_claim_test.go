package release

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func TestReleaseVersionClaimRefusesASecondReleaseAndNamesTheHolder(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)

	firstPID, secondPID := os.Getpid(), os.Getpid()+1

	firstHolder := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a"}
	if _, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, firstPID, now); err != nil {
		t.Fatalf("first release's claim: %v", err)
	}

	secondHolder := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b"}
	_, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.211", secondHolder, secondPID, now.Add(time.Second))
	if err == nil {
		t.Fatal("expected a second concurrent release of the same version to be refused")
	}
	refusal := shared.ReleaseVersionClaimRefusalError("1.0.211", err)
	if !strings.Contains(refusal.Error(), "1.0.211") {
		t.Errorf("refusal must name the version, got: %v", refusal)
	}
	if !strings.Contains(refusal.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the holder so a second orchestrator knows who to wait on, got: %v", refusal)
	}

	// The claim is version-scoped, not worktree-wide.
	if _, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.212", secondHolder, secondPID, now.Add(time.Second)); err != nil {
		t.Fatalf("a release of a different version must not collide with an unrelated one in flight: %v", err)
	}

	if _, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, firstPID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("the original holder renewing its own claim must succeed: %v", err)
	}
}

func TestReleaseVersionClaimReclaimsAnAbandonedRelease(t *testing.T) {
	isolateActivityCache(t)
	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)

	firstHolder := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a"}
	if _, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.211", firstHolder, os.Getpid(), start); err != nil {
		t.Fatalf("first release's claim: %v", err)
	}

	// A crashed holder's claim cannot be deleted from outside; it must lapse.
	afterLapse := start.Add(shared.ReleaseVersionClaimTTL + time.Minute)
	secondHolder := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b"}
	claim, err := shared.TakeReleaseVersionClaim("erun", "build", "1.0.211", secondHolder, os.Getpid()+1, afterLapse)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}
	if claim.Holder.Orchestrator != "orchestrator-b" {
		t.Fatalf("expected the new release to hold the claim, got %+v", claim)
	}
}
