package eruncommon

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The environment-scoped exclusive lease in release_claim_test.go passed
// while two orchestrators driving different environments still raced the
// same release, because its scope lived in each environment's own store.
// These tests exercise the repository-global claim directly against a real
// git remote, from two separate checkouts, so a scope mistake shows up the
// same way it did in production instead of passing by construction.

func newTestClaimContext() Context {
	return Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard)}
}

// cloneRepoForTest gives a second independent checkout of remote, standing in
// for a second environment's own worktree of the same repository.
func cloneRepoForTest(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, filepath.Dir(dir), "clone", "-q", remote, dir)
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	// The bare remote's own HEAD symref still names whatever
	// init.defaultBranch produced, not "main", so a fresh clone checks out
	// nothing; check out the branch this suite actually pushes explicitly.
	runGitForTest(t, dir, "checkout", "-q", "-b", "main", "--track", "origin/main")
	return dir
}

func TestReleaseRepoClaimRefusesASecondReleaseFromADifferentCheckoutAndNamesTheHolder(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	sha, err := takeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, now)
	if err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err = takeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, now.Add(time.Second))
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
	if _, err := takeReleaseRepoClaim(ctx, envB, "build-b", "1.0.214", holderB, now.Add(time.Second)); err != nil {
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

	holder := EnvironmentActivityLeaseHolder{Tenant: "erun"}
	if _, err := takeReleaseRepoClaim(ctx, envA, "build", "1.0.215", holder, now); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	_, err := takeReleaseRepoClaim(ctx, envB, "release", "1.0.215", holder, now.Add(time.Second))
	if err == nil {
		t.Fatal("expected a second release of the same version from a different environment to be refused")
	}
	if !strings.Contains(err.Error(), "environment build") {
		t.Errorf("refusal must name the losing caller's counterpart environment, not just its tenant, got: %v", err)
	}
}

// TestReleaseClaimHolderDescriptionRendersSensiblyWithMissingFields guards the
// rendering itself: either half of the holder/environment pair can be
// missing (an older claim record predating the environment field, or a
// holder with nothing else set), and the description must never leave an
// empty fragment or a dangling separator.
func TestReleaseClaimHolderDescriptionRendersSensiblyWithMissingFields(t *testing.T) {
	cases := []struct {
		name        string
		holder      EnvironmentActivityLeaseHolder
		environment string
		want        string
	}{
		{"holder and environment", EnvironmentActivityLeaseHolder{Tenant: "erun"}, "build", "tenant erun, environment build"},
		{"environment only, holder unnamed", EnvironmentActivityLeaseHolder{}, "build", "environment build"},
		{"holder only, no environment", EnvironmentActivityLeaseHolder{Tenant: "erun"}, "", "tenant erun"},
		{"neither", EnvironmentActivityLeaseHolder{}, "", "an unnamed holder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseClaimHolderDescription(tc.holder, tc.environment); got != tc.want {
				t.Errorf("releaseClaimHolderDescription(%+v, %q) = %q, want %q", tc.holder, tc.environment, got, tc.want)
			}
		})
	}
}

func TestReleaseRepoClaimReclaimsAnAbandonedReleaseFromADifferentCheckout(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := takeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, start); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	// envA crashed (or its pod was replaced) and never renewed or deleted its
	// claim. Nobody outside envA can remove the ref by hand, so the only way
	// envB's release ever proceeds is if the claim reclaims itself once it
	// lapses.
	afterLapse := start.Add(releaseVersionClaimTTL + time.Minute)
	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := takeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, afterLapse)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	remoteSHA, exists, err := gitLsRemoteRef(ctx, envB, releaseRepoClaimRemote, releaseRepoClaimRef("1.0.213"))
	if err != nil || !exists {
		t.Fatalf("expected the reclaimed ref to be published, exists=%v err=%v", exists, err)
	}
	if remoteSHA != sha {
		t.Fatalf("remote claim ref = %s, want the reclaiming holder's sha %s", remoteSHA, sha)
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
	holder := EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := takeReleaseRepoClaim(ctx, env, "build", "1.0.220", holder, now)
	if err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	record, err := readReleaseRepoClaimBlob(ctx, env, sha)
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
	holder := EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := takeReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, started)
	if err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	renewedAt := started.Add(13 * time.Minute)
	newSHA, err := renewReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, renewedAt, sha)
	if err != nil {
		t.Fatalf("renewal: %v", err)
	}

	record, err := readReleaseRepoClaimBlob(ctx, env, newSHA)
	if err != nil {
		t.Fatalf("reading back the renewed claim blob: %v", err)
	}
	if !record.StartedAt.Equal(started) {
		t.Errorf("renewal StartedAt = %v, want the original %v preserved", record.StartedAt, started)
	}
	wantExpiresAt := renewedAt.Add(releaseVersionClaimTTL)
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

	holderA := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := takeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	afterLapse := start.Add(releaseVersionClaimTTL + time.Minute)
	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := takeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, afterLapse)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}

	record, err := readReleaseRepoClaimBlob(ctx, envB, sha)
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

	holderA := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := takeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err := takeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, start.Add(15*time.Minute))
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
	spec := ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: ReleaseModeStable}
	command := ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	if err := runReleaseBranchPush(ctx, spec, command, GitCommandRunner); err != nil {
		t.Fatalf("runReleaseBranchPush: %v", err)
	}

	runGitForTest(t, repo, "fetch", "-q", "origin", "main")
	if err := runGitCheckForTest(repo, "merge-base", "--is-ancestor", "v1.0.213", "FETCH_HEAD"); err != nil {
		t.Fatalf("expected v1.0.213 to remain reachable from origin/main after the rebase, but it does not: %v", err)
	}
}

func runGitCheckForTest(dir string, args ...string) error {
	cmd := Command("git", append([]string{"-C", dir}, args...)...)
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
	spec := ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: ReleaseModeStable}
	command := ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	if err := runReleaseBranchPush(ctx, spec, command, GitCommandRunner); err != nil {
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
	spec := ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: ReleaseModeStable}
	command := ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	err := runReleaseBranchPush(ctx, spec, command, GitCommandRunner)
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
