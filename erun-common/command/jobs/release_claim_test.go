package jobs

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

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
	cancelled := activity.EnvironmentActivityLeaseHolder{Tenant: tenant}
	start := time.Now()

	cancelledSHA, err := eruncommon.TakeReleaseRepoClaim(ctx, repo, environment, version, cancelled, start, nil)
	if err != nil {
		t.Fatalf("cancelled release's repository claim: %v", err)
	}
	if _, err := eruncommon.TakeReleaseVersionClaim(tenant, environment, version, cancelled, exitedProcessPIDForTest(t), start); err != nil {
		t.Fatalf("cancelled release's local claim: %v", err)
	}

	// The leftovers are still live by the only rule the claim had before: it
	// names this environment and its process is gone, but its expiry is nearly
	// twenty minutes away. That window is what the report measured, and where
	// the refusal it described came from.
	if !time.Now().Before(readReleaseRepoClaimForTest(t, ctx, repo, cancelledSHA).ExpiresAt) {
		t.Fatal("the cancelled holder's claim must still be inside its TTL to reproduce the reported refusal")
	}

	spec := eruncommon.ReleaseSpec{ProjectRoot: repo, Version: version}
	release, err := eruncommon.ClaimReleaseVersion(ctx, spec, runtimePodEnvForTest(tenant, environment))
	if err != nil {
		t.Fatalf("the next attempt in this environment must reclaim a cancelled holder's claim, got: %v", err)
	}
	defer release()

	// The reclaim is a real one: the version's claim ref now names this
	// attempt, so a release elsewhere is refused from here on.
	sha, exists, err := eruncommon.GitLsRemoteRef(ctx, repo, eruncommon.ReleaseRepoClaimRemote, eruncommon.ReleaseRepoClaimRef(version))
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
func readReleaseRepoClaimForTest(t *testing.T, ctx core.Context, repo, sha string) eruncommon.ReleaseRepoClaimRecord {
	t.Helper()
	record, err := eruncommon.ReadReleaseRepoClaimBlob(ctx, repo, sha)
	if err != nil {
		t.Fatalf("reading back claim %s: %v", sha, err)
	}
	return record
}
