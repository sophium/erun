package eruncommon

import (
	"strings"
	"testing"
)

// A release tags locally as part of its recoverable, pre-publish stages
// (root AGENTS.md "Release Rules"). If it fails before publishing anything
// outward and origin/main advances before the retry — ordinary activity in a
// repo with a merge queue — the local tag no longer sits at HEAD, but it was
// also never pushed. canSkipExistingReleaseTag must recognize that as a
// leftover from the interrupted attempt and name the exact remedy, not just
// report a bare "already exists" mismatch.
func TestCanSkipExistingReleaseTagDiagnosesLeftoverLocalTagAfterOriginAdvances(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	// Attempt 1: release stamps a local commit + tag, then fails before
	// publishing anything outward (tag never pushed).
	writeAndCommit(t, repo, "release-stamp.txt", "v1.0.246\n", "chore: release 1.0.246")
	runGitForTest(t, repo, "tag", "-a", "v1.0.246", "-m", "v1.0.246")
	tagCommit, ok, err := gitResolvedRef(newTestClaimContext(), repo, "v1.0.246^{}")
	if err != nil || !ok {
		t.Fatalf("resolve tag commit: ok=%v err=%v", ok, err)
	}

	// Between attempts, an unrelated change lands on origin/main from a
	// second checkout -- ordinary activity in a repo with a merge queue.
	other := cloneRepoForTest(t, remote)
	writeAndCommit(t, other, "unrelated.txt", "unrelated\n", "unrelated merge")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	// Attempt 2: sync-remote fast-forwards local main onto the new origin
	// tip. The locally tagged commit is now behind HEAD and was never
	// pushed, so it drops out of main's history from this checkout's view.
	runGitForTest(t, repo, "fetch", "-q", "origin", "main")
	runGitForTest(t, repo, "reset", "-q", "--hard", "origin/main")
	headCommit, ok, err := gitResolvedRef(newTestClaimContext(), repo, "HEAD")
	if err != nil || !ok {
		t.Fatalf("resolve HEAD: ok=%v err=%v", ok, err)
	}
	if headCommit == tagCommit {
		t.Fatalf("expected HEAD to have moved past the tagged commit")
	}

	spec := ReleaseSpec{Version: "1.0.246", Branch: "main"}
	skip, err := canSkipExistingReleaseTag(newTestClaimContext(), spec, repo, "v1.0.246", GitCommandRunner)
	if err == nil {
		t.Fatalf("expected a mismatch error (skip=%v), got none", skip)
	}
	t.Logf("error returned: %s", err.Error())
	if !strings.Contains(err.Error(), "git tag -d v1.0.246") {
		t.Errorf("expected the leftover-tag remedy to be included, got: %s", err.Error())
	}
}
