package jobs

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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

// A retry after a failed, unpushed stamp resolves the version from the work commit.
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
	wantVersion := shared.ResolveReleaseVersion("1.0.268", workCommit, model.ReleaseModePrerelease)

	writeAndCommit(t, repo, "chart-version.txt", wantVersion+"\n", shared.ReleaseStampCommitPrefix+wantVersion)

	got, err := shared.ResolveReleaseVersionCommit(ctx, repo, "main", gitx.GitShortCommit, gitx.GitCommandRunner)
	if err != nil {
		t.Fatalf("resolveReleaseVersionCommit: %v", err)
	}
	if got != workCommit {
		t.Fatalf("expected the retry to resolve the real work commit %q, got %q", workCommit, got)
	}
	if gotVersion := shared.ResolveReleaseVersion("1.0.268", got, model.ReleaseModePrerelease); gotVersion != wantVersion {
		t.Fatalf("expected the retry to mint the original version %q, got %q", wantVersion, gotVersion)
	}
}

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

// A pushed stamp is a published release and must never be walked past.
func TestFindLeftoverReleaseStampBaseNeverWalksPastAPushedStamp(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	writeAndCommit(t, repo, "work.txt", "the actual work\n", "Transfer only the outputs whose content changed")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	writeAndCommit(t, repo, "chart-version.txt", "1.0.268\n", shared.ReleaseStampCommitPrefix+"1.0.268")
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
