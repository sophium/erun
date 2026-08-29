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
	sha, err := takeReleaseRepoClaim(ctx, envA, "1.0.213", holderA, os.Getpid(), now)
	if err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err = takeReleaseRepoClaim(ctx, envB, "1.0.213", holderB, os.Getpid()+1, now.Add(time.Second))
	if err == nil {
		t.Fatal("expected a second release of the same version from a different checkout to be refused")
	}
	if !strings.Contains(err.Error(), "1.0.213") {
		t.Errorf("refusal must name the version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the holder so the second environment knows who to wait on, got: %v", err)
	}

	// A release of a different version must not be affected: the claim is
	// scoped to the version's own ref, not the whole repository.
	if _, err := takeReleaseRepoClaim(ctx, envB, "1.0.214", holderB, os.Getpid()+1, now.Add(time.Second)); err != nil {
		t.Fatalf("a release of a different version must not collide with an unrelated one in flight: %v", err)
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
	if _, err := takeReleaseRepoClaim(ctx, envA, "1.0.213", holderA, os.Getpid(), start); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	// envA crashed (or its pod was replaced) and never renewed or deleted its
	// claim. Nobody outside envA can remove the ref by hand, so the only way
	// envB's release ever proceeds is if the claim reclaims itself once it
	// lapses.
	afterLapse := start.Add(releaseVersionClaimTTL + time.Minute)
	holderB := EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := takeReleaseRepoClaim(ctx, envB, "1.0.213", holderB, os.Getpid()+1, afterLapse)
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
