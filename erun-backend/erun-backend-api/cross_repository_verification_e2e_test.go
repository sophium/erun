package backendapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	eruncommon "github.com/sophium/erun/erun-common"
)

// A tenant serves more than one repository, and they share branch names — that
// is the whole reason a review records which repository its branches belong
// to. These gates drive two real (local, file://) remotes that both carry one
// target branch of the same name, against a real migrated Postgres, and check
// which repository a MERGED report is actually verified against.
//
// The row that matters is the one created before the platform recorded a
// repository: it carries none, so anything that resolves a repository for it
// has to resolve one it can genuinely be said to belong to, and never fall
// back to "every repository" — a filter the repository layer reads as no
// filter at all.

// newRemoteSharingBranch stands up a second, independent bare repository whose
// target branch carries the same name as another remote's, which is what makes
// the two collide in the platform's (targetBranch)-keyed bookkeeping.
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

// e2eOpenReviewInRepository opens a review that records the repository its
// branches belong to, the shape every review created since the platform
// started recording one has.
func e2eOpenReviewInRepository(t *testing.T, baseURL, name, repository, targetBranch, sourceBranch string) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews", map[string]any{
		"name":         fmt.Sprintf("%s %d", name, time.Now().UnixNano()),
		"repository":   repository,
		"targetBranch": targetBranch,
		"sourceBranch": sourceBranch,
	})
	if code != http.StatusCreated {
		t.Fatalf("create review: HTTP %d (want 201): %s", code, body)
	}
	var review mergeReviewResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &review), "parse review response")
	return review.ReviewID
}

// e2eReportMergedKeepingBody is e2eReportMerged with the response body kept,
// so a refusal can be reported in the platform's own words — which repository
// and which commit it measured the report against — rather than as a bare
// status code.
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

// e2eMergeAndReportGate drives one review through the last two steps an
// environment performs — merge the source onto the target for real, report the
// GATE build, then report MERGED — and returns the merge commit it produced
// alongside the report's own outcome.
func e2eMergeAndReportGate(t *testing.T, baseURL string, remote mergeQueueRemote, reviewID, sourceBranch, mergeMessage string) (string, int, string, mergeReviewResponse) {
	t.Helper()
	mergeCommit := remote.merge(t, remote.main, sourceBranch, mergeMessage)
	buildID := e2ePostGateBuild(t, baseURL, reviewID, mergeCommit, true, "")
	code, body, review := e2eReportMergedKeepingBody(t, baseURL, reviewID, buildID, remote.url)
	return mergeCommit, code, body, review
}

// forkRemote stands up a second bare remote that carries the first one's
// history, the shape two repositories of one tenant share when one was forked
// from the other — or when both descend from a common one.
func forkRemote(t *testing.T, origin mergeQueueRemote) mergeQueueRemote {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch="+origin.main)
	mirror := t.TempDir()
	runGit(t, mirror, "clone", "--mirror", origin.url, ".")
	runGit(t, mirror, "push", "--mirror", "file://"+bare)
	return mergeQueueRemote{url: "file://" + bare, main: origin.main}
}

// forceTargetInto replaces to's target branch with from's, discarding
// whatever to had there — the force-push a buggy or malicious reporter stands
// in for, since no well-behaved push can manufacture a replaced history.
func forceTargetInto(t *testing.T, from, to mergeQueueRemote) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", from.url, ".")
	runGit(t, dir, "push", "--force", to.url, from.main+":"+to.main)
}

// commitPresentIn reports whether commit exists anywhere in remote's fetched
// history. It is how these gates establish that the repositories they stand up
// really are disjoint: an anchor naming a commit absent from one of them can
// only have come from the other.
func commitPresentIn(t *testing.T, remote mergeQueueRemote, commit string) bool {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", remote.url, ".")
	cmd := exec.Command("git", "cat-file", "-e", commit)
	cmd.Dir = dir
	cmd.Env = append([]string{}, "HOME="+dir)
	return cmd.Run() == nil
}

// TestReportMergedDoesNotAnchorALegacyReviewOnAnotherRepositorysMergeCommit is
// the reproduction of the report this change fixes.
//
// Two repositories of one tenant both serve a target branch named the same.
// Repository A merges a review through the queue, so the platform records A's
// merge commit as the gated tip for that branch name. Repository B then merges
// a review of its own — a row created before the platform recorded a
// repository, so it carries none — and reports it.
//
// Repository B's queue has never merged onto this branch, so there is no gated
// tip in B's own history for the report to be measured against, and the report
// is a real, fully-verifiable landing. Anchoring it on the repository column
// rather than on the repository the report names resolved the tip through an
// empty filter — which the repository layer reads as "every repository" — and
// answered with A's merge commit, refusing B's landing because a stranger's
// commit is not one of its ancestors.
func TestReportMergedDoesNotAnchorALegacyReviewOnAnotherRepositorysMergeCommit(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	// One target branch name, served by two different repositories.
	target := uniqueBranchName(t, "shared-target")
	repoA := newRemoteSharingBranch(t, target)
	repoB := newRemoteSharingBranch(t, target)

	// Repository A merges a review of its own through the queue, moving the
	// platform's record of this target branch name forward.
	branchA := uniqueBranchName(t, "repo-a-feature")
	repoA.branch(t, branchA, "a.txt")
	reviewA := e2eOpenReviewInRepository(t, srv.URL, "cross-repo-a", repoA.url, target, branchA)
	e2eReportGreenBuild(t, srv.URL, reviewA)
	if status := readMergeReview(t, srv.URL, reviewA).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review A status = %s, want MERGE", status)
	}
	mergeA, code, body, merged := e2eMergeAndReportGate(t, srv.URL, repoA, reviewA, branchA, "merge "+branchA)
	if code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("review A did not merge: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	// Repository B merges a review of its own. It was created without a
	// repository — the legacy row shape — so the report's remote is the only
	// thing naming which repository this is.
	branchB := uniqueBranchName(t, "repo-b-feature")
	repoB.branch(t, branchB, "b.txt")
	reviewB := e2eOpenReview(t, srv.URL, "cross-repo-b", target, branchB)
	e2eReportGreenBuild(t, srv.URL, reviewB)
	if status := readMergeReview(t, srv.URL, reviewB).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review B status = %s, want MERGE", status)
	}

	mergeB, code, body, merged := e2eMergeAndReportGate(t, srv.URL, repoB, reviewB, branchB, "merge "+branchB)

	// The two repositories are genuinely disjoint: repository A's merge commit
	// is nowhere in repository B's history. Nothing about repository B can
	// therefore answer for it, and an anchor naming that commit — which is
	// exactly what the pre-fix refusal above names — could only have been
	// read out of repository A.
	if commitPresentIn(t, repoB, mergeA) {
		t.Fatalf("repository A's merge commit %s is present in repository B; these two remotes were meant to be disjoint", mergeA)
	}
	if code != http.StatusOK {
		t.Fatalf("review B's real landing (%s) was refused: HTTP %d: %s\nrepository B has never merged onto %s through the queue, so there is no gated tip in its own history to be measured against, and review A's merge commit is in a repository this review never recorded", mergeB, code, body, target)
	}
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("review B status = %s, want MERGED", merged.Status)
	}
	wantRepository, err := eruncommon.RepositoryIdentity(repoB.url)
	mustNoErr(t, err, "canonicalize repository B")
	if merged.Repository != wantRepository {
		t.Fatalf("review B repository = %q, want the repository its report named, %q", merged.Repository, wantRepository)
	}
}

// TestReportMergedVerifiesARecordedReviewAgainstItsOwnRepositoryOnly is the
// other half of that acceptance: a review that *does* record a repository
// keeps verifying against that one, and is still refused for a different one.
// The fix for the row above must not have been "stop telling repositories
// apart".
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
	buildID := e2ePostGateBuild(t, srv.URL, reviewID, mergeCommit, true, "")

	// Reporting the merge against a repository this review does not belong to
	// is refused, and refused without leaving the review somewhere else.
	if code, body, _ := e2eReportMergedKeepingBody(t, srv.URL, reviewID, buildID, repoB.url); code != http.StatusConflict {
		t.Fatalf("reporting the review's merge against repository B: HTTP %d (want %d): %s", code, http.StatusConflict, body)
	}
	if status := readMergeReview(t, srv.URL, reviewID).Status; status != model.ReviewStatusMerge {
		t.Fatalf("status after the refusal = %s, want the review left at MERGE", status)
	}

	// Its own repository is accepted, and the identity it recorded is the one
	// it keeps.
	code, body, merged := e2eReportMergedKeepingBody(t, srv.URL, reviewID, buildID, repoA.url)
	if code != http.StatusOK {
		t.Fatalf("reporting the review's merge against its own repository: HTTP %d: %s", code, body)
	}
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", merged.Status)
	}
	wantRepository, err := eruncommon.RepositoryIdentity(repoA.url)
	mustNoErr(t, err, "canonicalize repository A")
	if merged.Repository != wantRepository {
		t.Fatalf("repository = %q, want the review's own %q", merged.Repository, wantRepository)
	}
}

// TestReportMergedDoesNotLetAnotherRepositorysTipSatisfyTheGatedTipCheck is the
// other half of the same defect, and the reason it is not merely a false
// refusal.
//
// Two repositories share history. Repository B merges once through the queue,
// so the platform records B's own gated tip. Repository A then merges, more
// recently, and A's target is force-pushed over B's — replacing B's history, so
// the tip B was gated against is gone. A second, legacy review in B is gated
// against the replaced history and reports.
//
// Its reported commit descends from A's merge commit and not from B's own
// gated tip, which is exactly the rewrite condition 2 exists to refuse. Anchored
// on "every repository" it finds A's commit, an ancestor of the report, and
// accepts the rewrite; anchored on the repository the report names it finds
// B's own replaced tip and refuses.
func TestReportMergedDoesNotLetAnotherRepositorysTipSatisfyTheGatedTipCheck(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	target := uniqueBranchName(t, "shared-target")
	repoA := newRemoteSharingBranch(t, target)
	repoB := forkRemote(t, repoA)

	// Repository B merges once through the queue: this is B's own gated tip.
	branchB1 := uniqueBranchName(t, "repo-b-first")
	repoB.branch(t, branchB1, "b1.txt")
	reviewB1 := e2eOpenReviewInRepository(t, srv.URL, "fork-b-first", repoB.url, target, branchB1)
	e2eReportGreenBuild(t, srv.URL, reviewB1)
	mergeB1, code, body, merged := e2eMergeAndReportGate(t, srv.URL, repoB, reviewB1, branchB1, "merge "+branchB1)
	if code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("repository B's first merge did not land: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	// Repository A merges on its own target — more recently than B's, so a
	// cross-repository lookup would answer with A's commit.
	branchA := uniqueBranchName(t, "repo-a-feature")
	repoA.branch(t, branchA, "a.txt")
	reviewA := e2eOpenReviewInRepository(t, srv.URL, "fork-a", repoA.url, target, branchA)
	e2eReportGreenBuild(t, srv.URL, reviewA)
	if _, code, body, merged := e2eMergeAndReportGate(t, srv.URL, repoA, reviewA, branchA, "merge "+branchA); code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("repository A's merge did not land: HTTP %d status=%s: %s", code, merged.Status, body)
	}

	// A's target replaces B's: the tip B was gated against is gone.
	forceTargetInto(t, repoA, repoB)
	if commitPresentIn(t, repoB, mergeB1) {
		t.Fatalf("repository B's gated tip %s survived the force-push; the replaced history this test needs was not created", mergeB1)
	}

	// Repository B's next review is gated against the replaced history, and
	// reports a commit that descends from A's merge but not from B's own gated
	// tip. It carries no repository, so nothing but the report names B.
	branchB2 := uniqueBranchName(t, "repo-b-second")
	repoB.branch(t, branchB2, "b2.txt")
	reviewB2 := e2eOpenReview(t, srv.URL, "fork-b-second", target, branchB2)
	e2eReportGreenBuild(t, srv.URL, reviewB2)
	if status := readMergeReview(t, srv.URL, reviewB2).Status; status != model.ReviewStatusMerge {
		t.Fatalf("review B2 status = %s, want MERGE", status)
	}

	_, code, body, merged = e2eMergeAndReportGate(t, srv.URL, repoB, reviewB2, branchB2, "merge "+branchB2)
	if code != http.StatusConflict {
		t.Fatalf("a merge gated against a repository-rewritten target was accepted: HTTP %d status=%s: %s; repository B's own gated tip %s is not in its history any more, and another repository's commit is not evidence that it is", code, merged.Status, body, mergeB1)
	}
	if status := readMergeReview(t, srv.URL, reviewB2).Status; status != model.ReviewStatusMerge {
		t.Fatalf("status after the refusal = %s, want the review left at MERGE", status)
	}
}
