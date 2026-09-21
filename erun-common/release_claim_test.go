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

// TestClaimReleaseVersionReclaimsTheLeftoversOfACancelledRelease drives the
// production entry point, not the claim primitive, so the wiring that feeds the
// liveness probe is covered too — a reclaim the production path never asks for
// is not a fix. The leftovers it starts from are exactly what a release
// cancelled with SIGTERM leaves: both claims taken, neither dropped, and the
// local one still naming the process the signal ended.
func TestClaimReleaseVersionReclaimsTheLeftoversOfACancelledRelease(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	const tenant, environment, version = "erun", "build", "1.0.290"
	ctx := newTestClaimContext()
	cancelled := EnvironmentActivityLeaseHolder{Tenant: tenant}
	start := time.Now()

	cancelledSHA, err := takeReleaseRepoClaim(ctx, repo, environment, version, cancelled, start, nil)
	if err != nil {
		t.Fatalf("cancelled release's repository claim: %v", err)
	}
	if _, err := takeReleaseVersionClaim(tenant, environment, version, cancelled, exitedProcessPIDForTest(t), start); err != nil {
		t.Fatalf("cancelled release's local claim: %v", err)
	}

	// The leftovers are still live by the only rule the claim had before: it
	// names this environment and its process is gone, but its expiry is nearly
	// twenty minutes away. That window is what the report measured, and where
	// the refusal it described came from.
	if !time.Now().Before(readReleaseRepoClaimForTest(t, ctx, repo, cancelledSHA).ExpiresAt) {
		t.Fatal("the cancelled holder's claim must still be inside its TTL to reproduce the reported refusal")
	}

	spec := ReleaseSpec{ProjectRoot: repo, Version: version}
	release, err := claimReleaseVersion(ctx, spec, runtimePodEnvForTest(tenant, environment))
	if err != nil {
		t.Fatalf("the next attempt in this environment must reclaim a cancelled holder's claim, got: %v", err)
	}
	defer release()

	// The reclaim is a real one: the version's claim ref now names this
	// attempt, so a release elsewhere is refused from here on.
	sha, exists, err := gitLsRemoteRef(ctx, repo, releaseRepoClaimRemote, releaseRepoClaimRef(version))
	if err != nil || !exists {
		t.Fatalf("expected the reclaiming attempt to hold the version's claim, exists=%v err=%v", exists, err)
	}
	if !readReleaseRepoClaimForTest(t, ctx, repo, sha).StartedAt.After(start) {
		t.Errorf("the reclaim must be a fresh claim taken after the cancelled holder's own %v", start)
	}
}

// runtimePodEnvForTest is the environment a release reads its pod identity
// from, so claimReleaseVersion resolves a tenant and environment to claim in.
func runtimePodEnvForTest(tenant, environment string) func(string) string {
	return func(key string) string {
		switch key {
		case "ERUN_TENANT":
			return tenant
		case "ERUN_ENVIRONMENT":
			return environment
		}
		return ""
	}
}

// readReleaseRepoClaimForTest reads a claim blob back by its sha, failing the
// test rather than returning an error it would only ever report the same way.
func readReleaseRepoClaimForTest(t *testing.T, ctx Context, repo, sha string) releaseRepoClaimRecord {
	t.Helper()
	record, err := readReleaseRepoClaimBlob(ctx, repo, sha)
	if err != nil {
		t.Fatalf("reading back claim %s: %v", sha, err)
	}
	return record
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
