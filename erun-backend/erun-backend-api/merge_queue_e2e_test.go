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

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/jackc/pgx/v5/stdlib"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// mergeQueueE2E is the opt-in environment for the live merge-queue gate: this
// is the "MERGED means a merge actually happened and a build actually passed"
// property from a real Kubernetes Job, gated against a real migrated
// Postgres, running against a real checkout the operator pre-seeds. Unlike
// the release gate there is no dry-run mode — the whole point is proving the
// gate really pushes — so TargetBranch and the source branches MUST be
// disposable branches in the configured checkout, never a real integration
// branch.
type mergeQueueE2E struct {
	databaseURL       string
	dbosURL           string
	kubeconfig        string
	registry          string
	runtimeVersion    string
	namespace         string
	serviceAccount    string
	homeClaim         string
	workspaceClaim    string
	repoPath          string
	targetBranch      string
	sourceBranchA     string
	sourceBranchB     string
	conflictingBranch string
}

func mergeQueueE2EFromEnv(t *testing.T) mergeQueueE2E {
	t.Helper()
	if os.Getenv("ERUN_E2E_MERGE_QUEUE") != "1" {
		t.Skip("opt-in: set ERUN_E2E_MERGE_QUEUE=1 (+ a Kubernetes cluster, a migrated Postgres, and a disposable checkout the gate can fetch/build/push against)")
	}
	config := mergeQueueE2E{
		databaseURL:       os.Getenv("ERUN_E2E_MERGE_DATABASE_URL"),
		dbosURL:           os.Getenv("DBOS_SYSTEM_DATABASE_URL"),
		kubeconfig:        os.Getenv("ERUN_E2E_MERGE_KUBECONFIG"),
		registry:          os.Getenv("ERUN_E2E_MERGE_REGISTRY"),
		runtimeVersion:    os.Getenv("ERUN_E2E_MERGE_RUNTIME_VERSION"),
		namespace:         os.Getenv("ERUN_E2E_MERGE_NAMESPACE"),
		serviceAccount:    os.Getenv("ERUN_E2E_MERGE_SERVICE_ACCOUNT"),
		homeClaim:         os.Getenv("ERUN_E2E_MERGE_HOME_CLAIM"),
		workspaceClaim:    os.Getenv("ERUN_E2E_MERGE_WORKSPACE_CLAIM"),
		repoPath:          os.Getenv("ERUN_E2E_MERGE_REPO_PATH"),
		targetBranch:      os.Getenv("ERUN_E2E_MERGE_TARGET_BRANCH"),
		sourceBranchA:     os.Getenv("ERUN_E2E_MERGE_SOURCE_BRANCH_A"),
		sourceBranchB:     os.Getenv("ERUN_E2E_MERGE_SOURCE_BRANCH_B"),
		conflictingBranch: os.Getenv("ERUN_E2E_MERGE_CONFLICTING_SOURCE_BRANCH"),
	}
	for name, value := range map[string]string{
		"ERUN_E2E_MERGE_DATABASE_URL":              config.databaseURL,
		"DBOS_SYSTEM_DATABASE_URL":                 config.dbosURL,
		"ERUN_E2E_MERGE_KUBECONFIG":                config.kubeconfig,
		"ERUN_E2E_MERGE_REGISTRY":                  config.registry,
		"ERUN_E2E_MERGE_RUNTIME_VERSION":           config.runtimeVersion,
		"ERUN_E2E_MERGE_NAMESPACE":                 config.namespace,
		"ERUN_E2E_MERGE_SERVICE_ACCOUNT":           config.serviceAccount,
		"ERUN_E2E_MERGE_WORKSPACE_CLAIM":           config.workspaceClaim,
		"ERUN_E2E_MERGE_REPO_PATH":                 config.repoPath,
		"ERUN_E2E_MERGE_TARGET_BRANCH":             config.targetBranch,
		"ERUN_E2E_MERGE_SOURCE_BRANCH_A":           config.sourceBranchA,
		"ERUN_E2E_MERGE_SOURCE_BRANCH_B":           config.sourceBranchB,
		"ERUN_E2E_MERGE_CONFLICTING_SOURCE_BRANCH": config.conflictingBranch,
	} {
		if value == "" {
			t.Skipf("%s is required", name)
		}
	}
	return config
}

// startMergeQueueAPI wires the API the way the control plane runs it — real
// Kubernetes client, real Postgres, real durable workflow, real merge
// executor — and hands back the pieces a scenario asserts against.
func startMergeQueueAPI(t *testing.T, config mergeQueueE2E, appName string) (*httptest.Server, *sql.DB, kubernetes.Interface) {
	t.Helper()

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", config.kubeconfig)
	mustNoErr(t, err, "build kube config")
	kube, err := kubernetes.NewForConfig(kubeConfig)
	mustNoErr(t, err, "kube client")

	db, err := sql.Open("pgx", config.databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })
	dbosCtx, err := dbos.NewDBOSContext(context.Background(), dbos.Config{AppName: appName, DatabaseURL: config.dbosURL})
	mustNoErr(t, err, "dbos context")

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
		DBOSContext:   dbosCtx,
		KubeClient:    kube,
		Merge: provision.MergeConfig{
			Registry:       config.registry,
			RuntimeVersion: config.runtimeVersion,
			Namespace:      config.namespace,
			ServiceAccount: config.serviceAccount,
			HomeClaim:      config.homeClaim,
			WorkspaceClaim: config.workspaceClaim,
			RepoPath:       config.repoPath,
		},
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, db, kube
}

type mergeReviewResponse struct {
	ReviewID          string             `json:"reviewId"`
	Status            model.ReviewStatus `json:"status"`
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
// nothing else is merging on that target, promotes it straight to MERGE —
// which is what dispatches the merge-gate Job under test.
func e2eReportGreenBuild(t *testing.T, baseURL, reviewID string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews/"+reviewID+"/builds", map[string]any{
		"successful": true,
		"commitId":   "ci-" + reviewID,
		"version":    "0.0.1",
	})
	if code != http.StatusCreated {
		t.Fatalf("report green build for %s: HTTP %d: %s", reviewID, code, body)
	}
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

// awaitReviewTerminal polls until the merge gate's durable workflow lands the
// review on MERGED or FAILED.
func awaitReviewTerminal(t *testing.T, baseURL, reviewID string) mergeReviewResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		review := readMergeReview(t, baseURL, reviewID)
		if review.Status == model.ReviewStatusMerged || review.Status == model.ReviewStatusFailed {
			return review
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timed out waiting for review %s to reach a terminal status", reviewID)
	return mergeReviewResponse{}
}

// currentTargetCommit reads the target branch's tip directly from the
// checkout the merge Jobs push to, which is the ground truth for whether the
// target branch actually moved.
func currentTargetCommit(t *testing.T, repoPath, targetBranch string) string {
	t.Helper()
	fetch := exec.Command("git", "-C", repoPath, "fetch", "origin", targetBranch)
	if out, err := fetch.CombinedOutput(); err != nil {
		t.Fatalf("fetch origin %s: %v: %s", targetBranch, err, out)
	}
	rev := exec.Command("git", "-C", repoPath, "rev-parse", "FETCH_HEAD")
	out, err := rev.CombinedOutput()
	mustNoErr(t, err, "rev-parse FETCH_HEAD for "+targetBranch+": "+string(out))
	return strings.TrimSpace(string(out))
}

// TestMergeQueueLandsAReviewEndToEnd is the gate: a promotion to MERGE has to
// become a real Job in the cluster, run a real `erun build` against the real
// prospective merge, push it, and land the review on MERGED naming the build
// it merged with.
func TestMergeQueueLandsAReviewEndToEnd(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv, _, _ := startMergeQueueAPI(t, config, "erun-merge-queue-e2e")

	reviewID := e2eOpenReview(t, srv.URL, "merge-queue-e2e", config.targetBranch, config.sourceBranchA)
	e2eReportGreenBuild(t, srv.URL, reviewID)

	review := awaitReviewTerminal(t, srv.URL, reviewID)
	if review.Status != model.ReviewStatusMerged {
		t.Fatalf("review did not merge: status=%q", review.Status)
	}
	if review.LastMergedBuildID == "" {
		t.Fatal("review MERGED but named no gate build")
	}

	code, body := e2eRequest(t, srv.URL, http.MethodGet, "/v1/reviews/"+reviewID+"/builds/"+review.LastMergedBuildID, nil)
	if code != http.StatusOK {
		t.Fatalf("get gate build: HTTP %d: %s", code, body)
	}
	var build struct {
		Kind       model.BuildKind `json:"kind"`
		Successful bool            `json:"successful"`
		Version    string          `json:"version"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &build), "parse build response")
	if build.Kind != model.BuildKindGate || !build.Successful {
		t.Fatalf("gate build = %+v, want a successful GATE build", build)
	}
	if build.Version != "" {
		t.Fatalf("gate build named version %q, want none — the gate publishes nothing", build.Version)
	}
}

// TestMergeQueueCatchesTwoReviewsGreenAloneButBrokenTogether is the mandatory
// proof #1196 calls out: two reviews, each of which merges cleanly onto the
// target by itself, whose combination the target branch must never see. The
// first lands for real; the second, gated against the target the first left
// behind, fails for real, and the target branch never moves for it.
func TestMergeQueueCatchesTwoReviewsGreenAloneButBrokenTogether(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	if config.conflictingBranch == "" {
		t.Skip("ERUN_E2E_MERGE_CONFLICTING_SOURCE_BRANCH is required for this scenario")
	}
	srv, _, _ := startMergeQueueAPI(t, config, "erun-merge-queue-conflict-e2e")

	firstID := e2eOpenReview(t, srv.URL, "merge-queue-e2e-first", config.targetBranch, config.sourceBranchA)
	e2eReportGreenBuild(t, srv.URL, firstID)
	first := awaitReviewTerminal(t, srv.URL, firstID)
	if first.Status != model.ReviewStatusMerged {
		t.Fatalf("first review did not merge: status=%q", first.Status)
	}
	targetAfterFirst := currentTargetCommit(t, config.repoPath, config.targetBranch)

	// This branch was forked from the ORIGINAL target and independently is
	// green — it is the counterpart the operator seeds specifically to conflict
	// with sourceBranchA once A has already landed.
	secondID := e2eOpenReview(t, srv.URL, "merge-queue-e2e-second", config.targetBranch, config.conflictingBranch)
	e2eReportGreenBuild(t, srv.URL, secondID)
	second := awaitReviewTerminal(t, srv.URL, secondID)
	if second.Status != model.ReviewStatusFailed {
		t.Fatalf("the conflicting second review was accepted: status=%q", second.Status)
	}
	if second.LastFailedBuildID == "" {
		t.Fatal("the failed review named no gate build")
	}

	targetAfterSecond := currentTargetCommit(t, config.repoPath, config.targetBranch)
	if targetAfterSecond != targetAfterFirst {
		t.Fatalf("target branch moved after the rejected second merge: %s -> %s", targetAfterFirst, targetAfterSecond)
	}
}

// TestMergeQueueLandsTwoNonConflictingReviewsSequentially is the companion
// case: two reviews that do NOT conflict must both land for real, the second
// gated against the real target the first left behind.
func TestMergeQueueLandsTwoNonConflictingReviewsSequentially(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv, _, _ := startMergeQueueAPI(t, config, "erun-merge-queue-sequential-e2e")

	firstID := e2eOpenReview(t, srv.URL, "merge-queue-e2e-seq-first", config.targetBranch, config.sourceBranchA)
	e2eReportGreenBuild(t, srv.URL, firstID)
	first := awaitReviewTerminal(t, srv.URL, firstID)
	if first.Status != model.ReviewStatusMerged {
		t.Fatalf("first review did not merge: status=%q", first.Status)
	}

	secondID := e2eOpenReview(t, srv.URL, "merge-queue-e2e-seq-second", config.targetBranch, config.sourceBranchB)
	e2eReportGreenBuild(t, srv.URL, secondID)
	second := awaitReviewTerminal(t, srv.URL, secondID)
	if second.Status != model.ReviewStatusMerged {
		t.Fatalf("second review, which does not conflict with the first, did not merge: status=%q", second.Status)
	}
	if second.LastMergedBuildID == first.LastMergedBuildID {
		t.Fatal("both reviews recorded the same gate build")
	}
}
