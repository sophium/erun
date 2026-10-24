package backendapi

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// Repositories of one tenant share branch names, so a MERGED report must be
// verified against the reporting repository, never "every repository".

// newRemoteSharingBranch creates an independent remote with the same target branch name.
func newRemoteSharingBranch(t *testing.T, branch string) mergeQueueRemote {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch="+branch)
	seed := t.TempDir()
	runGit(t, seed, "init", "--initial-branch="+branch)
	runGit(t, seed, "commit", "--allow-empty", "-m", "root")
	runGit(t, seed, "remote", "add", "origin", "file://"+bare)
	runGit(t, seed, "push", "origin", branch)
	return mergeQueueRemote{url: "file://" + bare, main: branch}
}

// e2eReportMergedKeepingBody keeps the refusal body, which names the repository and commit measured.
func e2eReportMergedKeepingBody(t *testing.T, baseURL, reviewID, buildID, remoteURL string) (int, string, mergeReviewResponse) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPatch, "/v1/reviews/"+reviewID+"/status", map[string]any{
		"status":    "MERGED",
		"buildId":   buildID,
		"remoteUrl": remoteURL,
	})
	var review mergeReviewResponse
	if code == http.StatusOK {
		mustNoErr(t, json.Unmarshal([]byte(body), &review), "parse review response")
	}
	return code, body, review
}

// e2eMergeAndReportMerged merges for real, records the build, reports MERGED, and
// returns the merge commit.
func e2eMergeAndReportMerged(t *testing.T, baseURL string, remote mergeQueueRemote, reviewID, sourceBranch, mergeMessage string) (string, int, string, mergeReviewResponse) {
	t.Helper()
	mergeCommit := remote.merge(t, remote.main, sourceBranch, mergeMessage)
	buildID := e2ePostMergeBuild(t, baseURL, reviewID, mergeCommit)
	code, body, review := e2eReportMergedKeepingBody(t, baseURL, reviewID, buildID, remote.url)
	return mergeCommit, code, body, review
}

// forkRemote creates a second remote sharing the first one's history.
func forkRemote(t *testing.T, origin mergeQueueRemote) mergeQueueRemote {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch="+origin.main)
	mirror := t.TempDir()
	runGit(t, mirror, "clone", "--mirror", origin.url, ".")
	runGit(t, mirror, "push", "--mirror", "file://"+bare)
	return mergeQueueRemote{url: "file://" + bare, main: origin.main}
}

// forceTargetInto force-replaces to's target branch with from's.
func forceTargetInto(t *testing.T, from, to mergeQueueRemote) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", from.url, ".")
	runGit(t, dir, "push", "--force", to.url, from.main+":"+to.main)
}

func commitPresentIn(t *testing.T, remote mergeQueueRemote, commit string) bool {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", remote.url, ".")
	cmd := exec.Command("git", "cat-file", "-e", commit)
	cmd.Dir = dir
	cmd.Env = append([]string{}, "HOME="+dir)
	return cmd.Run() == nil
}

// A legacy review without a repository must be measured against the reporting
// repository's history, not another repository's tip for the same branch name.
func TestReportMergedDoesNotAnchorALegacyReviewOnAnotherRepositorysMergeCommit(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	target := uniqueBranchName(t, "shared-target")
	repoA := newRemoteSharingBranch(t, target)
	repoB := newRemoteSharingBranch(t, target)

	branchA := uniqueBranchName(t, "repo-a-feature")
	repoA.branch(t, branchA, "a.txt")
	reviewA := e2eOpenReviewInRepository(t, srv.URL, "cross-repo-a", repoA.url, target, branchA)
	e2eReportGreenBuild(t, srv.URL, reviewA)
	if status := readMergeReview(t, srv.URL, reviewA).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review A status = %s, want MERGE", status)
	}
	mergeA, code, body, merged := e2eMergeAndReportMerged(t, srv.URL, repoA, reviewA, branchA, "merge "+branchA)
	if code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("review A did not merge: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	// Legacy row without a repository: only the report's remote names it.
	branchB := uniqueBranchName(t, "repo-b-feature")
	repoB.branch(t, branchB, "b.txt")
	reviewB := e2eOpenReview(t, srv.URL, "cross-repo-b", target, branchB)
	e2eReportGreenBuild(t, srv.URL, reviewB)
	if status := readMergeReview(t, srv.URL, reviewB).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review B status = %s, want MERGE", status)
	}

	mergeB, code, body, merged := e2eMergeAndReportMerged(t, srv.URL, repoB, reviewB, branchB, "merge "+branchB)

	if commitPresentIn(t, repoB, mergeA) {
		t.Fatalf("repository A's merge commit %s is present in repository B; these two remotes were meant to be disjoint", mergeA)
	}
	if code != http.StatusOK {
		t.Fatalf("review B's real landing (%s) was refused: HTTP %d: %s\nrepository B has never merged onto %s through the queue, so there is no gated tip in its own history to be measured against, and review A's merge commit is in a repository this review never recorded", mergeB, code, body, target)
	}
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("review B status = %s, want MERGED", merged.Status)
	}
	wantRepository, err := modelpkg.RepositoryIdentity(repoB.url)
	mustNoErr(t, err, "canonicalize repository B")
	if merged.Repository != wantRepository {
		t.Fatalf("review B repository = %q, want the repository its report named, %q", merged.Repository, wantRepository)
	}
}

func TestReportMergedVerifiesARecordedReviewAgainstItsOwnRepositoryOnly(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	target := uniqueBranchName(t, "shared-target")
	repoA := newRemoteSharingBranch(t, target)
	repoB := newRemoteSharingBranch(t, target)

	sourceBranch := uniqueBranchName(t, "repo-a-feature")
	repoA.branch(t, sourceBranch, "a.txt")
	reviewID := e2eOpenReviewInRepository(t, srv.URL, "cross-repo-recorded", repoA.url, target, sourceBranch)
	e2eReportGreenBuild(t, srv.URL, reviewID)
	if status := readMergeReview(t, srv.URL, reviewID).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review status = %s, want MERGE", status)
	}

	mergeCommit := repoA.merge(t, target, sourceBranch, "merge "+sourceBranch)
	buildID := e2ePostMergeBuild(t, srv.URL, reviewID, mergeCommit)

	if code, body, _ := e2eReportMergedKeepingBody(t, srv.URL, reviewID, buildID, repoB.url); code != http.StatusConflict {
		t.Fatalf("reporting the review's merge against repository B: HTTP %d (want %d): %s", code, http.StatusConflict, body)
	}
	if status := readMergeReview(t, srv.URL, reviewID).Status; status != model.ReviewStatusMerge {
		t.Fatalf("status after the refusal = %s, want the review left at MERGE", status)
	}

	code, body, merged := e2eReportMergedKeepingBody(t, srv.URL, reviewID, buildID, repoA.url)
	if code != http.StatusOK {
		t.Fatalf("reporting the review's merge against its own repository: HTTP %d: %s", code, body)
	}
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", merged.Status)
	}
	wantRepository, err := modelpkg.RepositoryIdentity(repoA.url)
	mustNoErr(t, err, "canonicalize repository A")
	if merged.Repository != wantRepository {
		t.Fatalf("repository = %q, want the review's own %q", merged.Repository, wantRepository)
	}
}

// Another repository's tip must not satisfy the gated-tip check for a rewritten history.
func TestReportMergedDoesNotLetAnotherRepositorysTipSatisfyTheGatedTipCheck(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	target := uniqueBranchName(t, "shared-target")
	repoA := newRemoteSharingBranch(t, target)
	repoB := forkRemote(t, repoA)

	branchB1 := uniqueBranchName(t, "repo-b-first")
	repoB.branch(t, branchB1, "b1.txt")
	reviewB1 := e2eOpenReviewInRepository(t, srv.URL, "fork-b-first", repoB.url, target, branchB1)
	e2eReportGreenBuild(t, srv.URL, reviewB1)
	mergeB1, code, body, merged := e2eMergeAndReportMerged(t, srv.URL, repoB, reviewB1, branchB1, "merge "+branchB1)
	if code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("repository B's first merge did not land: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	// More recent than B's, so a cross-repository lookup would answer with A's commit.
	branchA := uniqueBranchName(t, "repo-a-feature")
	repoA.branch(t, branchA, "a.txt")
	reviewA := e2eOpenReviewInRepository(t, srv.URL, "fork-a", repoA.url, target, branchA)
	e2eReportGreenBuild(t, srv.URL, reviewA)
	if _, code, body, merged := e2eMergeAndReportMerged(t, srv.URL, repoA, reviewA, branchA, "merge "+branchA); code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("repository A's merge did not land: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	forceTargetInto(t, repoA, repoB)
	if commitPresentIn(t, repoB, mergeB1) {
		t.Fatalf("repository B's gated tip %s survived the force-push; the replaced history this test needs was not created", mergeB1)
	}

	// Descends from A's merge but not B's gated tip, and carries no repository.
	branchB2 := uniqueBranchName(t, "repo-b-second")
	repoB.branch(t, branchB2, "b2.txt")
	reviewB2 := e2eOpenReview(t, srv.URL, "fork-b-second", target, branchB2)
	e2eReportGreenBuild(t, srv.URL, reviewB2)
	if status := readMergeReview(t, srv.URL, reviewB2).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review B2 status = %s, want MERGE", status)
	}

	_, code, body, merged = e2eMergeAndReportMerged(t, srv.URL, repoB, reviewB2, branchB2, "merge "+branchB2)
	if code != http.StatusConflict {
		t.Fatalf("a merge gated against a repository-rewritten target was accepted: HTTP %d status=%s: %s; repository B's own gated tip %s is not in its history any more, and another repository's commit is not evidence that it is", code, merged.Status, body, mergeB1)
	}
	if status := readMergeReview(t, srv.URL, reviewB2).Status; status != model.ReviewStatusMerge {
		t.Fatalf("status after the refusal = %s, want the review left at MERGE", status)
	}
}
