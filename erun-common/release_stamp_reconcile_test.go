package eruncommon

import (
	"testing"
)

// A release that fails after its "release" stage stamp commit leaves that
// stamp sitting, unpushed, at HEAD. Root AGENTS.md "Release Rules" calls the
// pre-publish stages "recoverable by re-running", but re-running naively read
// HEAD's own commit to name the retry's version -- naming it after the stamp
// rather than the change being released, and stacking another stamp on top
// of it every further retry. These tests pin the fix's two
// load-bearing properties: version resolution walks past a leftover,
// unpushed stamp to mint the same version the first attempt did, and the
// walk stops at anything already pushed or incorporated by origin, matching
// the boundary releaseTagMismatchError already draws for the release tag.

// TestNewReleaseStageOmitsCommitWhenNoFileUpdatesAreNeeded pins the
// mechanism that keeps a retry from stacking a second stamp commit: once
// version resolution mints the same version the first attempt did (see the
// tests below), the chart/packaging files a retry would stamp are already at
// that version -- discoverReleaseCharts reports no diff, so the "release"
// stage must carry no add/commit commands at all, only the (already
// idempotent, via canSkipExistingReleaseTag) tag command.
func TestNewReleaseStageOmitsCommitWhenNoFileUpdatesAreNeeded(t *testing.T) {
	stage := newReleaseStage("/tmp/release-stamp-test", nil, "1.2.3", ReleaseModeStable)
	for _, command := range stage.GitCommands {
		if len(command.Args) > 0 && command.Args[0] == "commit" {
			t.Fatalf("expected no commit command when no file updates are needed, got %v", command.Args)
		}
	}
	if len(stage.GitCommands) != 1 || stage.GitCommands[0].Args[0] != "tag" {
		t.Fatalf("expected only the tag command, got %v", stage.GitCommands)
	}
}

// TestResolveReleaseVersionCommitUsesHeadWhenNoLeftoverStampExists is the
// first-release case: HEAD is ordinary work, not a stamp, so resolution must
// behave exactly as it always did.
func TestResolveReleaseVersionCommitUsesHeadWhenNoLeftoverStampExists(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	ctx := newTestClaimContext()
	want, err := GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit: %v", err)
	}
	got, err := resolveReleaseVersionCommit(ctx, repo, "main", GitShortCommit, GitCommandRunner)
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
	workCommit, err := GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit at work commit: %v", err)
	}
	wantVersion := resolveReleaseVersion("1.0.268", workCommit, ReleaseModePrerelease)

	// The failed first attempt's own stamp: committed locally, never pushed.
	writeAndCommit(t, repo, "chart-version.txt", wantVersion+"\n", releaseStampCommitPrefix+wantVersion)

	got, err := resolveReleaseVersionCommit(ctx, repo, "main", GitShortCommit, GitCommandRunner)
	if err != nil {
		t.Fatalf("resolveReleaseVersionCommit: %v", err)
	}
	if got != workCommit {
		t.Fatalf("expected the retry to resolve the real work commit %q, got %q", workCommit, got)
	}
	if gotVersion := resolveReleaseVersion("1.0.268", got, ReleaseModePrerelease); gotVersion != wantVersion {
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
	workCommit, err := GitShortCommit(ctx, repo)
	if err != nil {
		t.Fatalf("GitShortCommit at work commit: %v", err)
	}

	// Attempt 1's stamp, then attempt 2 (the first retry) stacking its own
	// stamp on top -- both unpushed.
	writeAndCommit(t, repo, "chart-version.txt", "1.0.268-pr.attempt1\n", releaseStampCommitPrefix+"1.0.268-pr.attempt1")
	writeAndCommit(t, repo, "chart-version.txt", "1.0.268-pr.attempt2\n", releaseStampCommitPrefix+"1.0.268-pr.attempt2")

	got, err := resolveReleaseVersionCommit(ctx, repo, "main", GitShortCommit, GitCommandRunner)
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

	writeAndCommit(t, repo, "chart-version.txt", "1.0.268\n", releaseStampCommitPrefix+"1.0.268")
	// Unlike the leftover scenarios above, this stamp is pushed -- a
	// genuinely completed and published release, not an interrupted one.
	runGitForTest(t, repo, "push", "-q", "origin", "main")

	ctx := newTestClaimContext()
	_, found, err := findLeftoverReleaseStampBase(ctx, repo, "main", GitCommandRunner)
	if err != nil {
		t.Fatalf("findLeftoverReleaseStampBase: %v", err)
	}
	if found {
		t.Fatalf("expected a pushed stamp to never be treated as a reclaimable leftover")
	}
}
