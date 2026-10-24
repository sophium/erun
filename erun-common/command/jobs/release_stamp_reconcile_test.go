package jobs

import (
	"testing"

	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// TestResolveReleaseVersionCommitUsesHeadWhenNoLeftoverStampExists is the
// first-release case: HEAD is ordinary work, not a stamp, so resolution must
// behave exactly as it always did.
func TestResolveReleaseVersionCommitUsesHeadWhenNoLeftoverStampExists(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	ctx := newTestClaimContext()
	want, err := gitx.GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit: %v", err)
	}
	got, err := shared.ResolveReleaseVersionCommit(ctx, repo, "main", gitx.GitShortCommit, gitx.GitCommandRunner)
	if err != nil {
		t.Fatalf("resolveReleaseVersionCommit: %v", err)
	}
	if got != want {
		t.Fatalf("expected HEAD's own commit %q, got %q", want, got)
	}
}

// TestResolveReleaseVersionCommitSkipsPastAnUnpushedLeftoverStampAndMintsTheOriginalVersion
// reproduces the issue's exact single-retry shape: a real work commit is
// pushed, then a release stamp commit lands on top and fails before
// publishing (so it is never pushed). The retry must resolve the version
// from the real work commit, not the stamp -- otherwise it mints a version
// named after the previous attempt instead of the change being released.
func TestResolveReleaseVersionCommitSkipsPastAnUnpushedLeftoverStampAndMintsTheOriginalVersion(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	writeAndCommit(t, repo, "work.txt", "the actual work\n", "Transfer only the outputs whose content changed")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	ctx := newTestClaimContext()
	workCommit, err := gitx.GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit at work commit: %v", err)
	}
	wantVersion := shared.ResolveReleaseVersion("1.0.268", workCommit, shared.ReleaseModePrerelease)

	// The failed first attempt's own stamp: committed locally, never pushed.
	writeAndCommit(t, repo, "chart-version.txt", wantVersion+"\n", shared.ReleaseStampCommitPrefix+wantVersion)

	got, err := shared.ResolveReleaseVersionCommit(ctx, repo, "main", gitx.GitShortCommit, gitx.GitCommandRunner)
	if err != nil {
		t.Fatalf("resolveReleaseVersionCommit: %v", err)
	}
	if got != workCommit {
		t.Fatalf("expected the retry to resolve the real work commit %q, got %q", workCommit, got)
	}
	if gotVersion := shared.ResolveReleaseVersion("1.0.268", got, shared.ReleaseModePrerelease); gotVersion != wantVersion {
		t.Fatalf("expected the retry to mint the original version %q, got %q", wantVersion, gotVersion)
	}
}

// TestResolveReleaseVersionCommitSkipsPastAChainOfUnpushedLeftoverStamps
// reproduces the observed state after two failed attempts: two unpushed
// stamp commits stacked on the real work commit. A second retry must still
// resolve the original work commit, not either stamp.
func TestResolveReleaseVersionCommitSkipsPastAChainOfUnpushedLeftoverStamps(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	writeAndCommit(t, repo, "work.txt", "the actual work\n", "Transfer only the outputs whose content changed")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	ctx := newTestClaimContext()
	workCommit, err := gitx.GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit at work commit: %v", err)
	}

	// Attempt 1's stamp, then attempt 2 (the first retry) stacking its own
	// stamp on top -- both unpushed.
	writeAndCommit(t, repo, "chart-version.txt", "1.0.268-pr.attempt1\n", shared.ReleaseStampCommitPrefix+"1.0.268-pr.attempt1")
	writeAndCommit(t, repo, "chart-version.txt", "1.0.268-pr.attempt2\n", shared.ReleaseStampCommitPrefix+"1.0.268-pr.attempt2")

	got, err := shared.ResolveReleaseVersionCommit(ctx, repo, "main", gitx.GitShortCommit, gitx.GitCommandRunner)
	if err != nil {
		t.Fatalf("resolveReleaseVersionCommit: %v", err)
	}
	if got != workCommit {
		t.Fatalf("expected the second retry to resolve the real work commit %q, got %q", workCommit, got)
	}
}

// TestFindLeftoverReleaseStampBaseNeverWalksPastAPushedStamp pins the
// boundary: a stamp that has been pushed, or that origin's history has
// incorporated, is a real, published commit -- not an interrupted attempt's
// leftover -- and must never be rewritten or walked past.
func TestFindLeftoverReleaseStampBaseNeverWalksPastAPushedStamp(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	writeAndCommit(t, repo, "work.txt", "the actual work\n", "Transfer only the outputs whose content changed")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	writeAndCommit(t, repo, "chart-version.txt", "1.0.268\n", shared.ReleaseStampCommitPrefix+"1.0.268")
	// Unlike the leftover scenarios above, this stamp is pushed -- a
	// genuinely completed and published release, not an interrupted one.
	runGitForTest(t, repo, "push", "-q", "origin", "main")

	ctx := newTestClaimContext()
	_, found, err := shared.FindLeftoverReleaseStampBase(ctx, repo, "main", gitx.GitCommandRunner)
	if err != nil {
		t.Fatalf("findLeftoverReleaseStampBase: %v", err)
	}
	if found {
		t.Fatalf("expected a pushed stamp to never be treated as a reclaimable leftover")
	}
}
