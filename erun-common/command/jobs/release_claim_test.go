package jobs

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Drives the production entry point against a SIGTERM-cancelled release's
// leftover claims, so the liveness wiring is covered too.
func TestClaimReleaseVersionReclaimsTheLeftoversOfACancelledRelease(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	const tenant, environment, version = "erun", "build", "1.0.290"
	ctx := newTestClaimContext()
	cancelled := activity.EnvironmentActivityLeaseHolder{Tenant: tenant}
	start := time.Now()

	cancelledSHA, err := shared.TakeReleaseRepoClaim(ctx, repo, environment, version, cancelled, start, nil)
	if err != nil {
		t.Fatalf("cancelled release's repository claim: %v", err)
	}
	if _, err := shared.TakeReleaseVersionClaim(tenant, environment, version, cancelled, exitedProcessPIDForTest(t), start); err != nil {
		t.Fatalf("cancelled release's local claim: %v", err)
	}

	// Process gone but expiry far off: the claim must still be reclaimed.
	if !time.Now().Before(readReleaseRepoClaimForTest(t, ctx, repo, cancelledSHA).ExpiresAt) {
		t.Fatal("the cancelled holder's claim must still be inside its TTL to reproduce the reported refusal")
	}

	spec := shared.ReleaseSpec{ProjectRoot: repo, Version: version}
	release, err := shared.ClaimReleaseVersion(ctx, spec, runtimePodEnvForTest(tenant, environment))
	if err != nil {
		t.Fatalf("the next attempt in this environment must reclaim a cancelled holder's claim, got: %v", err)
	}
	defer release()

	sha, exists, err := shared.GitLsRemoteRef(ctx, repo, shared.ReleaseRepoClaimRemote, shared.ReleaseRepoClaimRef(version))
	if err != nil || !exists {
		t.Fatalf("expected the reclaiming attempt to hold the version's claim, exists=%v err=%v", exists, err)
	}
	if !readReleaseRepoClaimForTest(t, ctx, repo, sha).StartedAt.After(start) {
		t.Errorf("the reclaim must be a fresh claim taken after the cancelled holder's own %v", start)
	}
}

// runtimePodEnvForTest sets the pod identity a release reads.
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

func readReleaseRepoClaimForTest(t *testing.T, ctx core.Context, repo, sha string) shared.ReleaseRepoClaimRecord {
	t.Helper()
	record, err := shared.ReadReleaseRepoClaimBlob(ctx, repo, sha)
	if err != nil {
		t.Fatalf("reading back claim %s: %v", sha, err)
	}
	return record
}
