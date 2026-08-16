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
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/jackc/pgx/v5/stdlib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/releaseexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// releaseQueueE2E is the opt-in environment for the live release-queue gate. Like
// the env-deploy gate it needs a real Kubernetes cluster and a migrated Postgres;
// unlike it, the work the Job does is a release, so it also needs a checkout the
// release can run against.
//
// The release target is deliberately scoped: ERUN_E2E_RELEASE_DRY_RUN=1 runs
// `erun release --dry-run`, which resolves the whole release — version, images,
// charts, git actions — without publishing anything or moving a public ref.
// Cutting a real erun version needs push credentials and makes public refs move,
// which is not something a test may do.
type releaseQueueE2E struct {
	databaseURL    string
	dbosURL        string
	kubeconfig     string
	registry       string
	runtimeVersion string
	namespace      string
	serviceAccount string
	homeClaim      string
	workspaceClaim string
	repoPath       string
	targetBranch   string
	commitID       string
	// secondCommit is a descendant of commitID on the same branch, so the
	// serialisation scenario can run two real releases back to back and see them
	// land on a coherent version line rather than racing.
	secondCommit string
	dryRun       bool
}

func releaseQueueE2EFromEnv(t *testing.T) releaseQueueE2E {
	t.Helper()
	if os.Getenv("ERUN_E2E_RELEASE_QUEUE") != "1" {
		t.Skip("opt-in: set ERUN_E2E_RELEASE_QUEUE=1 (+ a Kubernetes cluster, a migrated Postgres, and a checkout the release can run against)")
	}
	config := releaseQueueE2E{
		databaseURL:    os.Getenv("ERUN_E2E_RELEASE_DATABASE_URL"),
		dbosURL:        os.Getenv("DBOS_SYSTEM_DATABASE_URL"),
		kubeconfig:     os.Getenv("ERUN_E2E_RELEASE_KUBECONFIG"),
		registry:       os.Getenv("ERUN_E2E_RELEASE_REGISTRY"),
		runtimeVersion: os.Getenv("ERUN_E2E_RELEASE_RUNTIME_VERSION"),
		namespace:      os.Getenv("ERUN_E2E_RELEASE_NAMESPACE"),
		serviceAccount: os.Getenv("ERUN_E2E_RELEASE_SERVICE_ACCOUNT"),
		homeClaim:      os.Getenv("ERUN_E2E_RELEASE_HOME_CLAIM"),
		workspaceClaim: os.Getenv("ERUN_E2E_RELEASE_WORKSPACE_CLAIM"),
		repoPath:       os.Getenv("ERUN_E2E_RELEASE_REPO_PATH"),
		targetBranch:   os.Getenv("ERUN_E2E_RELEASE_TARGET_BRANCH"),
		commitID:       os.Getenv("ERUN_E2E_RELEASE_COMMIT"),
		secondCommit:   os.Getenv("ERUN_E2E_RELEASE_SECOND_COMMIT"),
		dryRun:         os.Getenv("ERUN_E2E_RELEASE_DRY_RUN") == "1",
	}
	for name, value := range map[string]string{
		"ERUN_E2E_RELEASE_DATABASE_URL":    config.databaseURL,
		"DBOS_SYSTEM_DATABASE_URL":         config.dbosURL,
		"ERUN_E2E_RELEASE_KUBECONFIG":      config.kubeconfig,
		"ERUN_E2E_RELEASE_REGISTRY":        config.registry,
		"ERUN_E2E_RELEASE_RUNTIME_VERSION": config.runtimeVersion,
		"ERUN_E2E_RELEASE_NAMESPACE":       config.namespace,
		"ERUN_E2E_RELEASE_SERVICE_ACCOUNT": config.serviceAccount,
		"ERUN_E2E_RELEASE_REPO_PATH":       config.repoPath,
		"ERUN_E2E_RELEASE_TARGET_BRANCH":   config.targetBranch,
		"ERUN_E2E_RELEASE_COMMIT":          config.commitID,
		"ERUN_E2E_RELEASE_SECOND_COMMIT":   config.secondCommit,
	} {
		if value == "" {
			t.Skipf("%s is required", name)
		}
	}
	if !config.dryRun {
		t.Skip("ERUN_E2E_RELEASE_DRY_RUN=1 is required: this gate must not cut a real release, which would move public refs")
	}
	return config
}

// startReleaseQueueAPI wires the API the way the control plane runs it — real
// Kubernetes client, real Postgres, real durable workflow, real release
// executor — and hands back the pieces a scenario asserts against.
func startReleaseQueueAPI(t *testing.T, config releaseQueueE2E, appName string) (*httptest.Server, *sql.DB, kubernetes.Interface) {
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
		Release: provision.ReleaseConfig{
			Registry:       config.registry,
			RuntimeVersion: config.runtimeVersion,
			Namespace:      config.namespace,
			ServiceAccount: config.serviceAccount,
			HomeClaim:      config.homeClaim,
			WorkspaceClaim: config.workspaceClaim,
			RepoPath:       config.repoPath,
			DryRun:         config.dryRun,
		},
	})
	mustNoErr(t, err, "new handler")
	mustNoErr(t, dbos.Launch(dbosCtx), "dbos launch")
	t.Cleanup(func() { dbos.Shutdown(dbosCtx, 5*time.Second) })

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { deleteReleaseJobs(t, kube, config.namespace) })
	return srv, db, kube
}

// TestReleaseQueueRunsAQueuedReleaseEndToEnd is the gate: a trigger has to become
// a real Job in the cluster, run the real `erun release`, and land the release
// row on `released` naming the version the run minted, with the build recorded
// against the review that earned it.
func TestReleaseQueueRunsAQueuedReleaseEndToEnd(t *testing.T) {
	config := releaseQueueE2EFromEnv(t)
	srv, _, kube := startReleaseQueueAPI(t, config, "erun-release-queue-e2e")

	reviewID := e2eReadyReview(t, srv.URL, "release-queue-e2e", config.targetBranch)
	before := releaseJobNames(t, kube, config.namespace)

	releaseID := e2eTriggerRelease(t, srv.URL, reviewID, config.targetBranch, config.commitID, http.StatusCreated)
	release := awaitReleaseTerminal(t, srv.URL, releaseID)
	if release.Status != model.ReleaseStatusReleased {
		t.Fatalf("release did not publish: status=%q reason=%s", release.Status, release.FailureReason)
	}
	if strings.TrimSpace(release.Version) == "" {
		t.Fatal("the release published but named no version, so nothing can say what was released")
	}
	t.Logf("release %s published version %s", releaseID, release.Version)

	// The version has to have come from a Job that actually ran in the cluster.
	after := releaseJobNames(t, kube, config.namespace)
	if len(after) <= len(before) {
		t.Fatalf("no release job ran: jobs before %v, after %v", before, after)
	}

	// The build the release produced is recorded against the review that earned it.
	assertBuildRecorded(t, srv.URL, reviewID, config.commitID, release.Version)

	// Re-triggering the same merge commit must mint nothing.
	assertRetriggerMintsNothing(t, srv.URL, kube, config, reviewID, release)
}

// assertBuildRecorded holds the recording half of the pipeline: the release's
// build lands on the review with the commit it ran on and the version it minted.
func assertBuildRecorded(t *testing.T, baseURL, reviewID, commitID, version string) {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/reviews/"+reviewID+"/builds", nil)
	if code != http.StatusOK {
		t.Fatalf("list builds: HTTP %d: %s", code, body)
	}
	var builds []struct {
		CommitID   string `json:"commitId"`
		Version    string `json:"version"`
		Successful bool   `json:"successful"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &builds), "parse builds response")
	for _, build := range builds {
		if build.CommitID == commitID && build.Version == version && build.Successful {
			return
		}
	}
	t.Fatalf("no build recorded for commit %s at version %s: %s", commitID, version, body)
}

// assertRetriggerMintsNothing is the idempotency contract. Two versions for one
// merge commit is the worst thing this queue could do, so a repeat trigger must
// answer with the release that already exists and run no new Job.
func assertRetriggerMintsNothing(t *testing.T, baseURL string, kube kubernetes.Interface, config releaseQueueE2E, reviewID string, released releaseResponse) {
	t.Helper()
	before := releaseJobNames(t, kube, config.namespace)
	againID := e2eTriggerRelease(t, baseURL, reviewID, config.targetBranch, config.commitID, http.StatusOK)
	if againID != released.ReleaseID {
		t.Fatalf("re-trigger produced release %s, want the one already released %s", againID, released.ReleaseID)
	}
	again := readRelease(t, baseURL, againID)
	if again.Version != released.Version {
		t.Fatalf("re-trigger changed the version from %q to %q", released.Version, again.Version)
	}
	if again.Attempt != released.Attempt {
		t.Fatalf("re-trigger bumped the attempt on a released commit: %d -> %d", released.Attempt, again.Attempt)
	}
	// A Job started here would be a second release for one commit.
	waitForNoNewReleaseJob(t, kube, config.namespace, before)
}

// waitForNoNewReleaseJob gives a would-be second release long enough to appear.
// Asserting immediately would pass even if the re-trigger had started one.
func waitForNoNewReleaseJob(t *testing.T, kube kubernetes.Interface, namespace string, before []string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		after := releaseJobNames(t, kube, namespace)
		if len(after) > len(before) {
			t.Fatalf("re-triggering an already-released commit started a second release job: before %v, after %v", before, after)
		}
		time.Sleep(3 * time.Second)
	}
}

// TestReleaseQueueRunsTwoTriggersSequentially is the serialisation contract.
// `erun release` bumps a semver, writes version-bearing files, tags and pushes,
// so two concurrent releases on one version line corrupt it. Two triggers landing
// close together must produce two runs one after the other, never two at once.
func TestReleaseQueueRunsTwoTriggersSequentially(t *testing.T) {
	config := releaseQueueE2EFromEnv(t)
	srv, db, kube := startReleaseQueueAPI(t, config, "erun-release-queue-serial-e2e")

	first := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, config.commitID, http.StatusCreated)
	second := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, config.secondCommit, http.StatusCreated)

	// The queue must never hold two running rows for one tenant. Sampling while
	// both are in flight is what proves the second waited rather than raced.
	assertNeverTwoRunning(t, db, srv.URL, []string{first, second})

	versions := make([]string, 0, 2)
	for _, releaseID := range []string{first, second} {
		release := awaitReleaseTerminal(t, srv.URL, releaseID)
		if release.Status != model.ReleaseStatusReleased {
			t.Fatalf("release %s did not publish: status=%q reason=%s", releaseID, release.Status, truncate(release.FailureReason))
		}
		t.Logf("release %s published %s", releaseID, release.Version)
		versions = append(versions, release.Version)
	}
	// Two releases on one version line: each names its own version, and neither
	// re-used the other's. A race would have produced one version twice, or a
	// version for a commit the other release had already moved past.
	if versions[0] == versions[1] {
		t.Fatalf("both releases published %q, so they did not run on a coherent version line", versions[0])
	}
	// Each ran as its own Job, so neither replayed the other.
	if jobs := releaseJobNames(t, kube, config.namespace); len(jobs) < 2 {
		t.Fatalf("release jobs = %v, want one per release", jobs)
	}
}

// assertNeverTwoRunning samples the queue until both releases are terminal,
// failing the moment a tenant holds two running rows at once.
func assertNeverTwoRunning(t *testing.T, db *sql.DB, baseURL string, releaseIDs []string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		var running int
		err := db.QueryRow(`SELECT COUNT(*) FROM releases WHERE status = 'running'`).Scan(&running)
		mustNoErr(t, err, "count running releases")
		if running > 1 {
			t.Fatalf("%d releases were running at once, so the per-tenant serial queue did not hold", running)
		}
		terminal := 0
		for _, releaseID := range releaseIDs {
			if readRelease(t, baseURL, releaseID).Status.Terminal() {
				terminal++
			}
		}
		if terminal == len(releaseIDs) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("timed out waiting for both releases to reach a terminal state")
}

// TestReleaseQueueRecordsAFailureAndKeepsGoing: a release that cannot run must
// leave a reason an operator can act on, and must not wedge the queue behind it.
// A commit that is not in the checkout is the cheapest real failure to produce —
// the run's own `git merge --ff-only` refuses it.
func TestReleaseQueueRecordsAFailureAndKeepsGoing(t *testing.T) {
	config := releaseQueueE2EFromEnv(t)
	srv, _, _ := startReleaseQueueAPI(t, config, "erun-release-queue-failure-e2e")

	bad := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, "0000000000000000000000000000000000000000", http.StatusCreated)
	good := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, config.commitID+"-after-failure", http.StatusCreated)

	failed := awaitReleaseTerminal(t, srv.URL, bad)
	if failed.Status != model.ReleaseStatusFailed {
		t.Fatalf("a release of a commit that is not in the checkout did not fail: status=%q", failed.Status)
	}
	t.Logf("recorded failureReason:\n%s", failed.FailureReason)
	// A reason that names only the Job's exit is the opaque failure this gate
	// exists to reject; the run's own words have to survive into the row.
	if !strings.Contains(failed.FailureReason, "0000000000000000000000000000000000000000") {
		t.Fatalf("failureReason does not name the commit that could not be released:\n%s", failed.FailureReason)
	}
	if len(strings.TrimSpace(failed.FailureReason)) <= len("release job failed for "+config.targetBranch) {
		t.Fatalf("failureReason carries no detail beyond the job outcome:\n%s", failed.FailureReason)
	}
	if failed.Version != "" {
		t.Fatalf("a failed release recorded version %q", failed.Version)
	}

	// The next release still runs: a failure must not block the queue.
	next := awaitReleaseTerminal(t, srv.URL, good)
	if next.Status == model.ReleaseStatusQueued || next.Status == model.ReleaseStatusRunning {
		t.Fatalf("the release behind a failed one never finished: status=%q", next.Status)
	}
	t.Logf("release after the failure reached %s (version %q)", next.Status, next.Version)
}

// TestReleaseQueueRequeuesAFailedCommit: a transient failure must not lock a
// commit out of ever being released, and the retry must be a new attempt so its
// Job runs rather than replaying the previous one.
func TestReleaseQueueRequeuesAFailedCommit(t *testing.T) {
	config := releaseQueueE2EFromEnv(t)
	srv, _, _ := startReleaseQueueAPI(t, config, "erun-release-queue-requeue-e2e")

	commit := "0000000000000000000000000000000000000001"
	releaseID := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, commit, http.StatusCreated)
	failed := awaitReleaseTerminal(t, srv.URL, releaseID)
	if failed.Status != model.ReleaseStatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}

	againID := e2eTriggerRelease(t, srv.URL, "", config.targetBranch, commit, http.StatusOK)
	if againID != releaseID {
		t.Fatalf("re-trigger created release %s, want the same row %s requeued", againID, releaseID)
	}
	retried := awaitReleaseTerminal(t, srv.URL, againID)
	if retried.Attempt <= failed.Attempt {
		t.Fatalf("attempt = %d, want a new attempt past %d", retried.Attempt, failed.Attempt)
	}
}

type releaseResponse struct {
	ReleaseID     string              `json:"releaseId"`
	Status        model.ReleaseStatus `json:"status"`
	Attempt       int                 `json:"attempt"`
	Version       string              `json:"version"`
	FailureReason string              `json:"failureReason"`
}

func e2eTriggerRelease(t *testing.T, baseURL, reviewID, targetBranch, commitID string, wantCode int) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/releases", map[string]any{
		"reviewId":     reviewID,
		"targetBranch": targetBranch,
		"commitId":     commitID,
	})
	if code != wantCode {
		t.Fatalf("trigger release for %s: HTTP %d (want %d): %s", commitID, code, wantCode, body)
	}
	var release releaseResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &release), "parse release response")
	if release.ReleaseID == "" {
		t.Fatalf("trigger returned no release id: %s", body)
	}
	return release.ReleaseID
}

func readRelease(t *testing.T, baseURL, releaseID string) releaseResponse {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/releases/"+releaseID, nil)
	if code != http.StatusOK {
		t.Fatalf("get release %s: HTTP %d: %s", releaseID, code, body)
	}
	var release releaseResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &release), "parse release response")
	return release
}

// awaitReleaseTerminal polls until the durable workflow reports a terminal state.
func awaitReleaseTerminal(t *testing.T, baseURL, releaseID string) releaseResponse {
	t.Helper()
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		release := readRelease(t, baseURL, releaseID)
		if release.Status.Terminal() {
			return release
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timed out waiting for release %s to reach a terminal state", releaseID)
	return releaseResponse{}
}

// e2eReadyReview registers a review with a successful build, which is what a
// release records its own build against.
func e2eReadyReview(t *testing.T, baseURL, name, targetBranch string) string {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/reviews", map[string]any{
		"name":         fmt.Sprintf("%s %d", name, time.Now().UnixNano()),
		"targetBranch": targetBranch,
		"sourceBranch": name,
	})
	if code != http.StatusCreated {
		t.Fatalf("create review: HTTP %d (want 201): %s", code, body)
	}
	var review struct {
		ReviewID string `json:"reviewId"`
	}
	mustNoErr(t, json.Unmarshal([]byte(body), &review), "parse review response")
	return review.ReviewID
}

func releaseJobNames(t *testing.T, kube kubernetes.Interface, namespace string) []string {
	t.Helper()
	jobs, err := kube.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=" + releaseexec.ManagedByLabel,
	})
	mustNoErr(t, err, "list release jobs")
	names := make([]string, 0, len(jobs.Items))
	for _, job := range jobs.Items {
		names = append(names, job.Name)
	}
	return names
}

// deleteReleaseJobs clears the Jobs a scenario left behind, so a run does not
// leave its pods sitting in the operator's cluster until the TTL reaps them.
func deleteReleaseJobs(t *testing.T, kube kubernetes.Interface, namespace string) {
	t.Helper()
	jobs, err := kube.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=" + releaseexec.ManagedByLabel,
	})
	if err != nil {
		t.Logf("listing release jobs to clean up: %v", err)
		return
	}
	policy := metav1.DeletePropagationBackground
	for _, job := range jobs.Items {
		if err := kube.BatchV1().Jobs(namespace).Delete(context.Background(), job.Name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
			t.Logf("deleting release job %s: %v", job.Name, err)
		}
	}
}

func truncate(s string) string {
	if len(s) <= 300 {
		return s
	}
	return s[:300] + "…"
}
