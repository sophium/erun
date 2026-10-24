package jobs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// The environment-scoped exclusive lease in release_claim_test.go passed
// while two orchestrators driving different environments still raced the
// same release, because its scope lived in each environment's own store.
// These tests exercise the repository-global claim directly against a real
// git remote, from two separate checkouts, so a scope mistake shows up the
// same way it did in production instead of passing by construction.

func newTestClaimContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)}
}

// cloneRepoForTest gives a second independent checkout of remote, standing in
// for a second environment's own worktree of the same repository.
func cloneRepoForTest(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, filepath.Dir(dir), "clone", "-q", "--branch", "main", remote, dir)
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	return dir
}

func TestReleaseRepoClaimRefusesASecondReleaseFromADifferentCheckoutAndNamesTheHolder(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	sha, err := shared.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, now, nil)
	if err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	holderB := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err = shared.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, now.Add(time.Second), nil)
	if err == nil {
		t.Fatal("expected a second release of the same version from a different checkout to be refused")
	}
	if !strings.Contains(err.Error(), "1.0.213") {
		t.Errorf("refusal must name the version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the holder so the second environment knows who to wait on, got: %v", err)
	}
	if !strings.Contains(err.Error(), "build-a") {
		t.Errorf("refusal must name the losing caller's counterpart environment, got: %v", err)
	}

	// A release of a different version must not be affected: the claim is
	// scoped to the version's own ref, not the whole repository.
	if _, err := shared.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.214", holderB, now.Add(time.Second), nil); err != nil {
		t.Fatalf("a release of a different version must not collide with an unrelated one in flight: %v", err)
	}
}

// TestReleaseRepoClaimRefusalNamesTheEnvironmentWhenTheOrchestratorIDIsUnset
// reproduces the shape a real release actually writes (erun#1637):
// ERUN_ORCHESTRATOR_ID is unset in every in-pod release, so the holder
// carries only a tenant. Two environments of that same tenant racing the
// same version must still be told apart in the refusal — "tenant erun"
// alone does not say which environment to go look at.
func TestReleaseRepoClaimRefusalNamesTheEnvironmentWhenTheOrchestratorIDIsUnset(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holder := activity.EnvironmentActivityLeaseHolder{Tenant: "erun"}
	if _, err := shared.TakeReleaseRepoClaim(ctx, envA, "build", "1.0.215", holder, now, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	_, err := shared.TakeReleaseRepoClaim(ctx, envB, "release", "1.0.215", holder, now.Add(time.Second), nil)
	if err == nil {
		t.Fatal("expected a second release of the same version from a different environment to be refused")
	}
	if !strings.Contains(err.Error(), "environment build") {
		t.Errorf("refusal must name the losing caller's counterpart environment, not just its tenant, got: %v", err)
	}
}

func TestReleaseRepoClaimReclaimsAnAbandonedReleaseFromADifferentCheckout(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := shared.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	// envA crashed (or its pod was replaced) and never renewed or deleted its
	// claim. Nobody outside envA can remove the ref by hand, so the only way
	// envB's release ever proceeds is if the claim reclaims itself once it
	// lapses.
	afterLapse := start.Add(shared.ReleaseVersionClaimTTL + time.Minute)
	holderB := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := shared.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, afterLapse, nil)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	remoteSHA, exists, err := shared.GitLsRemoteRef(ctx, envB, shared.ReleaseRepoClaimRemote, shared.ReleaseRepoClaimRef("1.0.213"))
	if err != nil || !exists {
		t.Fatalf("expected the reclaimed ref to be published, exists=%v err=%v", exists, err)
	}
	if remoteSHA != sha {
		t.Fatalf("remote claim ref = %s, want the reclaiming holder's sha %s", remoteSHA, sha)
	}
}

// exitedProcessPIDForTest returns the pid of a process that has already
// exited, standing in for the release process a SIGTERM ended. It is a real
// pid, just one no liveness probe can succeed on — which is the state a
// cancelled release leaves its environment's local claim in.
func exitedProcessPIDForTest(t *testing.T) int {
	t.Helper()
	cmd := core.Command("git", "--version")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a stand-in process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for the stand-in process: %v", err)
	}
	return pid
}

// TestReleaseRepoClaimStillRefusesWhileThisEnvironmentsHolderIsAlive is the
// half of the liveness rule that keeps it a safety net rather than a
// formality: a claim naming this environment whose holder is still running
// must keep refusing a second release here, and the local claim is what says
// so. It is the same signal that reclaims a dead holder's claim, so this test
// is what stops that signal from being read as "nobody here is releasing".
func TestReleaseRepoClaimStillRefusesWhileThisEnvironmentsHolderIsAlive(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	const tenant, environment, version = "erun", "build", "1.0.289"
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	ctx := newTestClaimContext()

	running := activity.EnvironmentActivityLeaseHolder{Tenant: tenant, Orchestrator: "orchestrator-a"}
	if _, err := shared.TakeReleaseVersionClaim(tenant, environment, version, running, os.Getpid(), start); err != nil {
		t.Fatalf("running release's local claim: %v", err)
	}
	if _, err := shared.TakeReleaseRepoClaim(ctx, repo, environment, version, running, start, nil); err != nil {
		t.Fatalf("running release's repository claim: %v", err)
	}

	retry := start.Add(time.Minute)
	localHolderAlive := func() bool {
		held, err := shared.EnvironmentHasLiveReleaseHolder(tenant, environment, version, retry)
		if err != nil {
			t.Fatalf("reading this environment's release claims: %v", err)
		}
		return held
	}
	if !localHolderAlive() {
		t.Fatal("a live local holder must read as present")
	}

	second := activity.EnvironmentActivityLeaseHolder{Tenant: tenant, Orchestrator: "orchestrator-b"}
	_, err := shared.TakeReleaseRepoClaim(ctx, repo, environment, version, second, retry, localHolderAlive)
	if err == nil {
		t.Fatal("expected a second release in the same environment to be refused while the first is running")
	}
	if !strings.Contains(err.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the running holder, got: %v", err)
	}
}

// TestReleaseRepoClaimFreshClaimSetsStartedAtToNow guards the baseline a
// renewal must not disturb: a brand-new claim has nothing to inherit, so its
// StartedAt is the moment it was taken.
func TestReleaseRepoClaimFreshClaimSetsStartedAtToNow(t *testing.T) {
	env := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, env)
	runGitForTest(t, env, "push", "-q", "-u", "origin", "main")

	now := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()
	holder := activity.EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := shared.TakeReleaseRepoClaim(ctx, env, "build", "1.0.220", holder, now, nil)
	if err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	record, err := shared.ReadReleaseRepoClaimBlob(ctx, env, sha)
	if err != nil {
		t.Fatalf("reading back the claim blob: %v", err)
	}
	if !record.StartedAt.Equal(now) {
		t.Errorf("fresh claim StartedAt = %v, want %v", record.StartedAt, now)
	}
}

// TestReleaseRepoClaimRenewalPreservesStartedAtAndAdvancesOnlyExpiresAt is the
// regression test for erun#1668: a renewal used to rewrite StartedAt to the
// renewal time, so a release running for an hour with periodic renewals
// always looked a couple of minutes old in the claim blob.
func TestReleaseRepoClaimRenewalPreservesStartedAtAndAdvancesOnlyExpiresAt(t *testing.T) {
	env := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, env)
	runGitForTest(t, env, "push", "-q", "-u", "origin", "main")

	started := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()
	holder := activity.EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := shared.TakeReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, started, nil)
	if err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	renewedAt := started.Add(13 * time.Minute)
	newSHA, err := shared.RenewReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, renewedAt, sha)
	if err != nil {
		t.Fatalf("renewal: %v", err)
	}

	record, err := shared.ReadReleaseRepoClaimBlob(ctx, env, newSHA)
	if err != nil {
		t.Fatalf("reading back the renewed claim blob: %v", err)
	}
	if !record.StartedAt.Equal(started) {
		t.Errorf("renewal StartedAt = %v, want the original %v preserved", record.StartedAt, started)
	}
	wantExpiresAt := renewedAt.Add(shared.ReleaseVersionClaimTTL)
	if !record.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf("renewal ExpiresAt = %v, want %v", record.ExpiresAt, wantExpiresAt)
	}
}

// TestReleaseRepoClaimReclaimOfAnExpiredHolderSetsANewStartedAt is the test
// that stops the fix above from over-correcting: a reclaim takes over from a
// dead holder, so it must NOT carry that holder's StartedAt forward — it is
// a different holder, and preserving the old value would make a genuinely
// wedged-looking claim (the thing StartedAt exists to reveal) look however
// old the abandoned holder happened to be.
func TestReleaseRepoClaimReclaimOfAnExpiredHolderSetsANewStartedAt(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := shared.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	afterLapse := start.Add(shared.ReleaseVersionClaimTTL + time.Minute)
	holderB := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := shared.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, afterLapse, nil)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}

	record, err := shared.ReadReleaseRepoClaimBlob(ctx, envB, sha)
	if err != nil {
		t.Fatalf("reading back the reclaiming holder's blob: %v", err)
	}
	if !record.StartedAt.Equal(afterLapse) {
		t.Errorf("reclaim StartedAt = %v, want the new holder's own claim time %v, not the abandoned holder's %v", record.StartedAt, afterLapse, start)
	}
}

// TestReleaseRepoClaimRefusalReportsHowLongTheHolderHasBeenRunning guards the
// refusal message added alongside the StartedAt fix: once the field means
// what its name says, the refusal can tell an operator how long the holder
// has actually been running, which is the number that says whether to wait
// or go investigate.
func TestReleaseRepoClaimRefusalReportsHowLongTheHolderHasBeenRunning(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := shared.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	holderB := activity.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err := shared.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, start.Add(15*time.Minute), nil)
	if err == nil {
		t.Fatal("expected the second claim to be refused")
	}
	if !strings.Contains(err.Error(), "running for 15m0s") {
		t.Errorf("refusal must report how long the holder has been running, got: %v", err)
	}
}

// TestReleaseBranchPushRebaseKeepsTheReleaseTagOnTheTargetBranch reproduces
// the second defect from the same run: the branch push absorbs a moved base
// branch by rebasing and retrying, but the release tag was already published
// under the pre-rebase commit. Left unfixed, the tag names a commit reachable
// from nothing once the rebase replays it under a new sha.
func TestReleaseBranchPushRebaseKeepsTheReleaseTagOnTheTargetBranch(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	// The release's own commit and its already-published tag, as they exist
	// once ensureReleaseReadyToPublish has passed and push-release-tag has run.
	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.213\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")
	runGitForTest(t, repo, "tag", "-a", "v1.0.213", "-m", "Release 1.0.213")
	runGitForTest(t, repo, "push", "-q", "origin", "v1.0.213")

	// An unrelated commit lands on origin/main while this release is still
	// running, so this checkout's eventual push is rejected. It touches a
	// different file than the release's own commits, the same way an
	// unrelated mainline change would in production — the rebase this test
	// exercises is about any base-branch move, not specifically a race
	// between two releases of the same version.
	other := cloneRepoForTest(t, remote)
	if err := os.WriteFile(filepath.Join(other, "OTHER.txt"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file in the other checkout: %v", err)
	}
	runGitForTest(t, other, "add", "OTHER.txt")
	runGitForTest(t, other, "commit", "-q", "-m", "unrelated mainline change")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	// This checkout's own post-release-version-bump commit, made on top of
	// the now-stale base before the push discovers the rejection.
	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.214-next\n"), 0o644); err != nil {
		t.Fatalf("write VERSION bump: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] prepare 1.0.214-next")

	ctx := newTestClaimContext()
	spec := shared.ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: shared.ReleaseModeStable}
	command := shared.ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	if err := shared.RunReleaseBranchPush(ctx, spec, command, gitx.GitCommandRunner); err != nil {
		t.Fatalf("runReleaseBranchPush: %v", err)
	}

	runGitForTest(t, repo, "fetch", "-q", "origin", "main")
	if err := runGitCheckForTest(repo, "merge-base", "--is-ancestor", "v1.0.213", "FETCH_HEAD"); err != nil {
		t.Fatalf("expected v1.0.213 to remain reachable from origin/main after the rebase, but it does not: %v", err)
	}
}

func runGitCheckForTest(dir string, args ...string) error {
	cmd := core.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run()
}

// TestReleaseBranchPushRebaseIgnoresAnOlderCommitWithTheSameSubjectOutsideTheRebasedRange
// guards the bound itself: an older commit sharing the release commit's exact
// subject, already common history before this release ever started, must not
// be mistaken for the commit the rebase just replayed.
func TestReleaseBranchPushRebaseIgnoresAnOlderCommitWithTheSameSubjectOutsideTheRebasedRange(t *testing.T) {
	repo := newAgentJobTestRepo(t)

	// A prior release of the same version, already part of history before
	// this run starts. It carries the exact subject this run's own release
	// commit will carry too, but it sits outside anything a rebase replays.
	if err := os.WriteFile(filepath.Join(repo, "OLD.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	runGitForTest(t, repo, "add", "OLD.txt")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")

	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.213\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")
	runGitForTest(t, repo, "tag", "-a", "v1.0.213", "-m", "Release 1.0.213")
	runGitForTest(t, repo, "push", "-q", "origin", "v1.0.213")

	other := cloneRepoForTest(t, remote)
	if err := os.WriteFile(filepath.Join(other, "OTHER.txt"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file in the other checkout: %v", err)
	}
	runGitForTest(t, other, "add", "OTHER.txt")
	runGitForTest(t, other, "commit", "-q", "-m", "unrelated mainline change")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.214-next\n"), 0o644); err != nil {
		t.Fatalf("write VERSION bump: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] prepare 1.0.214-next")

	ctx := newTestClaimContext()
	spec := shared.ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: shared.ReleaseModeStable}
	command := shared.ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	if err := shared.RunReleaseBranchPush(ctx, spec, command, gitx.GitCommandRunner); err != nil {
		t.Fatalf("runReleaseBranchPush: %v", err)
	}

	runGitForTest(t, repo, "fetch", "-q", "origin", "main")
	if err := runGitCheckForTest(repo, "merge-base", "--is-ancestor", "v1.0.213", "FETCH_HEAD"); err != nil {
		t.Fatalf("expected v1.0.213 to remain reachable from origin/main after the rebase, but it does not: %v", err)
	}
}

// TestReleaseBranchPushRebaseRefusesAnAmbiguousReplayedTagSubject guards the
// refusal itself: two commits inside the range the rebase just replayed share
// the tagged commit's exact subject, and the repoint must refuse rather than
// silently retag onto whichever one it happened to see first.
func TestReleaseBranchPushRebaseRefusesAnAmbiguousReplayedTagSubject(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.213\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")
	runGitForTest(t, repo, "tag", "-a", "v1.0.213", "-m", "Release 1.0.213")
	runGitForTest(t, repo, "push", "-q", "origin", "v1.0.213")

	// A second commit that happens to carry the exact same subject as the
	// tagged one, replayed by the same rebase alongside it.
	if err := os.WriteFile(filepath.Join(repo, "DUPLICATE.txt"), []byte("duplicate\n"), 0o644); err != nil {
		t.Fatalf("write duplicate-subject file: %v", err)
	}
	runGitForTest(t, repo, "add", "DUPLICATE.txt")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")

	other := cloneRepoForTest(t, remote)
	if err := os.WriteFile(filepath.Join(other, "OTHER.txt"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file in the other checkout: %v", err)
	}
	runGitForTest(t, other, "add", "OTHER.txt")
	runGitForTest(t, other, "commit", "-q", "-m", "unrelated mainline change")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	ctx := newTestClaimContext()
	spec := shared.ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: shared.ReleaseModeStable}
	command := shared.ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	err := shared.RunReleaseBranchPush(ctx, spec, command, gitx.GitCommandRunner)
	if err == nil {
		t.Fatal("expected an ambiguous replayed tag subject to refuse rather than silently retag")
	}
	if !strings.Contains(err.Error(), "v1.0.213") {
		t.Errorf("refusal must name the tag, got: %v", err)
	}
	if !strings.Contains(err.Error(), "release 1.0.213") {
		t.Errorf("refusal must name the subject, got: %v", err)
	}
	if !strings.Contains(err.Error(), "by hand") {
		t.Errorf("refusal must tell the operator to move the tag by hand, got: %v", err)
	}
}
