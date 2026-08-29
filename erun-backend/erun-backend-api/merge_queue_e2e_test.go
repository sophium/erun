package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// The merge queue no longer runs anything itself: the environment
// that promotes a review to MERGE does the fetch/merge/build/push locally,
// with its own already-warm checkout and daemon, then reports the outcome.
// This gate proves the platform's half of that contract — accepting a
// verified MERGED report and refusing an unverified one — against a real
// migrated Postgres and a real (local, file://) git remote. No cluster, no
// DBOS workflow, and no Kubernetes Job are involved, because none exist
// anymore for this to be a gate on.
type mergeQueueE2E struct {
	databaseURL string
}

func mergeQueueE2EFromEnv(t *testing.T) mergeQueueE2E {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_MERGE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_MERGE_DATABASE_URL to a migrated PostgreSQL")
	}
	return mergeQueueE2E{databaseURL: databaseURL}
}

// startMergeQueueAPI wires the API the way the control plane runs it for
// reviews/builds: real Postgres, real git verification. No KubeClient and no
// DBOSContext — neither is needed once nothing here dispatches a Job.
func startMergeQueueAPI(t *testing.T, config mergeQueueE2E) *httptest.Server {
	t.Helper()

	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, errors.New("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
	})
	mustNoErr(t, err, "new handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// runGit runs a git command and fails the test on error — these are real
// local git repositories with no network or cluster involved.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{}, "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// mergeQueueRemote is a bare local git repository standing in for the
// tenant's real remote, reachable only over file:// — exactly what the
// gitverify.RemoteVerifier fetches from, just without a real host.
type mergeQueueRemote struct {
	url string
	// main is a unique branch name per remote, not the literal "main": the
	// platform's "target tip it was gated against" bookkeeping is keyed by
	// (tenant, targetBranch) alone, so two tests both targeting a branch
	// literally named "main" — even in two different physical remotes —
	// would otherwise be compared against each other, which is a testing
	// artifact, not something that happens in reality (one tenant has one
	// remote per target branch name).
	main string
}

func newMergeQueueRemote(t *testing.T) mergeQueueRemote {
	t.Helper()
	main := uniqueBranchName(t, "main")
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch="+main)
	seed := t.TempDir()
	runGit(t, seed, "init", "--initial-branch="+main)
	runGit(t, seed, "commit", "--allow-empty", "-m", "root")
	runGit(t, seed, "remote", "add", "origin", "file://"+bare)
	runGit(t, seed, "push", "origin", main)
	return mergeQueueRemote{url: "file://" + bare, main: main}
}

// branch forks a new branch from main with one commit, pushes it, and
// returns its commit hash.
func (r mergeQueueRemote) branch(t *testing.T, name, filename string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", r.url, ".")
	runGit(t, dir, "checkout", "-b", name)
	mustNoErr(t, os.WriteFile(dir+"/"+filename, []byte(name), 0o644), "write "+filename)
	runGit(t, dir, "add", filename)
	runGit(t, dir, "commit", "-m", "add "+filename)
	runGit(t, dir, "push", "origin", name)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// mainTip reads the remote's current main tip.
func (r mergeQueueRemote) mainTip(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", r.url, ".")
	return runGit(t, dir, "rev-parse", r.main)
}

// merge is exactly what an environment does on promotion to MERGE: fetch
// target and source, squash-merge the source onto the current target, commit,
// and push. Real git, against the real (local) remote.
func (r mergeQueueRemote) merge(t *testing.T, targetBranch, sourceBranch, message string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "clone", r.url, ".")
	runGit(t, dir, "fetch", "origin", targetBranch, sourceBranch)
	runGit(t, dir, "checkout", "-B", targetBranch, "origin/"+targetBranch)
	runGit(t, dir, "merge", "--squash", "origin/"+sourceBranch)
	runGit(t, dir, "commit", "-m", message)
	commit := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "push", "origin", "HEAD:"+targetBranch)
	return commit
}

// uniqueBranchName keeps a re-run against a persistent database from
// colliding on the one-live-review-per-branch-pair uniqueness constraint, and
// keeps two tests' target branches from being compared against each other by
// the platform's own (tenant, targetBranch) gated-tip bookkeeping.
func uniqueBranchName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

type mergeReviewResponse struct {
	ReviewID          string             `json:"reviewId"`
	Status            model.ReviewStatus `json:"status"`
	TargetBranch      string             `json:"targetBranch"`
	LastMergedBuildID string             `json:"lastMergedBuildId"`
	LastFailedBuildID string             `json:"lastFailedBuildId"`
}

func e2eOpenReview(t *testing.T, baseURL, name, targetBranch, sourceBranch string) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews", map[string]any{
		"name":         fmt.Sprintf("%s %d", name, time.Now().UnixNano()),
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

// e2eReportGreenBuild is the CLIENT-reported build a real CI would post for
// the source branch. A successful one moves the review OPEN -> READY and, if
// nothing else is merging on that target, promotes it straight to MERGE.
func e2eReportGreenBuild(t *testing.T, baseURL, reviewID string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews/"+reviewID+"/builds", map[string]any{
		"successful": true,
		"commitId":   fmt.Sprintf("%040x", time.Now().UnixNano()),
		"version":    "0.0.1",
	})
	if code != http.StatusCreated {
		t.Fatalf("report green build for %s: HTTP %d: %s", reviewID, code, body)
	}
}

// e2ePostGateBuild is the environment reporting its own gate outcome —
// GATE builds are no longer written only by the platform.
func e2ePostGateBuild(t *testing.T, baseURL, reviewID, commit string, successful bool, failureDetail string) string {
	t.Helper()
	body := map[string]any{"kind": "GATE", "commitId": commit, "successful": successful}
	if failureDetail != "" {
		body["failureDetail"] = failureDetail
	}
	code, respBody := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews/"+reviewID+"/builds", body)
	if code != http.StatusCreated {
		t.Fatalf("report gate build for %s: HTTP %d: %s", reviewID, code, respBody)
	}
	var build struct {
		BuildID string `json:"buildId"`
	}
	mustNoErr(t, json.Unmarshal([]byte(respBody), &build), "parse build response")
	return build.BuildID
}

// e2eReportMerged is the environment's own claim that the merge landed —
// accepted only once the platform can verify it against the real repository.
func e2eReportMerged(t *testing.T, baseURL, reviewID, buildID, remoteURL string) (int, mergeReviewResponse) {
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
	return code, review
}

func readMergeReview(t *testing.T, baseURL, reviewID string) mergeReviewResponse {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/reviews/"+reviewID, nil)
	if code != http.StatusOK {
		t.Fatalf("get review %s: HTTP %d: %s", reviewID, code, body)
	}
	var review mergeReviewResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &review), "parse review response")
	return review
}

// TestMergeQueueAcceptsAVerifiedMergeEndToEnd is the environment-driven
// happy path: promote to MERGE, do the real merge/push exactly as an
// environment would, report the gate build, and have MERGED accepted because
// it checks out against the real repository — not because of who reported it.
func TestMergeQueueAcceptsAVerifiedMergeEndToEnd(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	sourceBranch := uniqueBranchName(t, "feature-a")
	remote.branch(t, sourceBranch, "a.txt")

	reviewID := e2eOpenReview(t, srv.URL, "merge-queue-e2e", remote.main, sourceBranch)
	e2eReportGreenBuild(t, srv.URL, reviewID)
	review := readMergeReview(t, srv.URL, reviewID)
	if review.Status != model.ReviewStatusMerge {
		t.Fatalf("review status = %s, want MERGE (queue was empty, so it should have promoted itself)", review.Status)
	}

	mergeCommit := remote.merge(t, remote.main, sourceBranch, "merge "+sourceBranch)
	buildID := e2ePostGateBuild(t, srv.URL, reviewID, mergeCommit, true, "")

	code, merged := e2eReportMerged(t, srv.URL, reviewID, buildID, remote.url)
	if code != http.StatusOK {
		t.Fatalf("report MERGED: HTTP %d", code)
	}
	if merged.Status != model.ReviewStatusMerged {
		t.Fatalf("status = %s, want MERGED", merged.Status)
	}
	if merged.LastMergedBuildID != buildID {
		t.Fatalf("lastMergedBuildId = %s, want %s", merged.LastMergedBuildID, buildID)
	}
}

// TestMergeQueueRefusesAMergeReportedAgainstAStaleTarget is the property the
// merge queue exists for, re-homed to the new architecture: a merge built
// against a target tip that is no longer current must never become MERGED,
// even though the reported commit really is (now) on the branch. Producing
// this without a second, well-behaved environment racing the first means
// force-pushing a merge computed against a stale local fetch — standing in
// for a buggy or malicious reporter, which is exactly who this check has to
// hold up against once any caller may report MERGED.
func TestMergeQueueRefusesAMergeReportedAgainstAStaleTarget(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)
	remote := newMergeQueueRemote(t)
	branchA := uniqueBranchName(t, "feature-a")
	branchC := uniqueBranchName(t, "feature-c")
	remote.branch(t, branchA, "a.txt")
	remote.branch(t, branchC, "c.txt")

	// feature-c's "environment" fetches main while it is still at the root
	// commit — before feature-a lands.
	staleClone := t.TempDir()
	runGit(t, staleClone, "clone", remote.url, ".")
	runGit(t, staleClone, "fetch", "origin", remote.main, branchC)
	runGit(t, staleClone, "checkout", "-B", remote.main, "origin/"+remote.main)
	runGit(t, staleClone, "merge", "--squash", "origin/"+branchC)
	runGit(t, staleClone, "commit", "-m", "merge "+branchC)
	staleMergeCommit := runGit(t, staleClone, "rev-parse", "HEAD")

	// Meanwhile feature-a's review lands for real, moving the platform's own
	// record of main's tip forward.
	reviewA := e2eOpenReview(t, srv.URL, "merge-queue-e2e-a", remote.main, branchA)
	e2eReportGreenBuild(t, srv.URL, reviewA)
	mergeCommitA := remote.merge(t, remote.main, branchA, "merge "+branchA)
	buildA := e2ePostGateBuild(t, srv.URL, reviewA, mergeCommitA, true, "")
	if code, merged := e2eReportMerged(t, srv.URL, reviewA, buildA, remote.url); code != http.StatusOK || merged.Status != model.ReviewStatusMerged {
		t.Fatalf("review A did not merge: HTTP %d status=%s", code, merged.Status)
	}

	// feature-c's stale merge still pushes — a fast-forward check alone does
	// not catch this, since force-pushing (a buggy or malicious reporter,
	// not a well-behaved one) can still land it on the branch.
	runGit(t, staleClone, "push", "--force", "origin", "HEAD:"+remote.main)
	if got := remote.mainTip(t); got != staleMergeCommit {
		t.Fatalf("main tip = %s, want the force-pushed stale merge %s to have landed", got, staleMergeCommit)
	}

	reviewC := e2eOpenReview(t, srv.URL, "merge-queue-e2e-c", remote.main, branchC)
	e2eReportGreenBuild(t, srv.URL, reviewC)
	buildC := e2ePostGateBuild(t, srv.URL, reviewC, staleMergeCommit, true, "")

	code, _ := e2eReportMerged(t, srv.URL, reviewC, buildC, remote.url)
	if code != http.StatusConflict {
		t.Fatalf("report MERGED for a stale-target merge: HTTP %d, want 409 MERGE_NOT_VERIFIED", code)
	}
	if got := readMergeReview(t, srv.URL, reviewC).Status; got == model.ReviewStatusMerged {
		t.Fatal("review C reached MERGED despite its merge being built against a target tip that was no longer current")
	}
}
