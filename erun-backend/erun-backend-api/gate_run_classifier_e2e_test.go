package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// gate_run_classifier_e2e_test.go verifies, against a real migrated
// Postgres and a real erun-backend-api handler (no dry-run, no mocked
// classifier), that RunGateRunStart/RunGateRunReport's known-infrastructure
// classifier (erun-common/gate_run_failure_classifier.go) actually changes
// what gets persisted and read back: a known signature must land as
// INCONCLUSIVE, a genuine failure must stay FAILED,
// and a caller reading either transport's shape back must be able to tell
// the two apart. Nothing here calls the classifier's own unexported
// function directly -- every case drives the same eruncommon.RunGateRunStart
// /RunGateRunReport entry points the CLI and MCP tools call, against a real
// HTTP round trip, exactly like merge_queue_e2e_test.go does for the merge
// queue.
type gateRunClassifierE2E struct {
	databaseURL string
}

func gateRunClassifierE2EFromEnv(t *testing.T) gateRunClassifierE2E {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_GATE_RUN_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_GATE_RUN_DATABASE_URL to a migrated PostgreSQL")
	}
	return gateRunClassifierE2E{databaseURL: databaseURL}
}

func startGateRunClassifierAPI(t *testing.T, config gateRunClassifierE2E) string {
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
	return srv.URL
}

// stubCloudReadStore hands newPlatformClientForAlias a single erun-type
// alias pointed at the real handler above -- no config file on disk, no
// real OIDC issuer, matching how a caller with a configured `erun cloud
// init erun` alias looks from RunGateRunStart's point of view.
type stubCloudReadStore struct {
	config eruncommon.ERunConfig
}

func (s stubCloudReadStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.config, "", nil
}

// stubCloudSecretStore is an in-memory CloudSecretStore. Pre-seeding the
// cached-access-token ref lets the token minter short-circuit straight to
// e2eDevToken without driving a real OIDC refresh grant -- the classifier
// under test lives entirely in RunGateRunStart/RunGateRunReport and the real
// HTTP/DB round trip, not in how a token gets minted.
type stubCloudSecretStore struct {
	secrets map[string]string
}

func (s *stubCloudSecretStore) SaveCloudSecret(ref, value string) error {
	s.secrets[ref] = value
	return nil
}

func (s *stubCloudSecretStore) LoadCloudSecret(ref string) (string, error) {
	return s.secrets[ref], nil
}

func (s *stubCloudSecretStore) DeleteCloudSecret(ref string) error {
	delete(s.secrets, ref)
	return nil
}

const gateRunClassifierE2EAlias = "gate-run-classifier-e2e"

// gateRunClassifierE2EDeps builds a store/deps pair whose sole configured
// erun alias resolves, with no network call, to a bearer token the real
// handler's stub TokenVerifier accepts.
func gateRunClassifierE2EDeps(t *testing.T, apiURL string) (eruncommon.CloudReadStore, eruncommon.CloudDependencies) {
	t.Helper()
	secrets := &stubCloudSecretStore{secrets: map[string]string{}}
	cachedToken := fmt.Sprintf(`{"accessToken":%q,"expiresAt":%q}`, e2eDevToken, time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	secrets.secrets["erun/access/"+gateRunClassifierE2EAlias] = cachedToken

	store := stubCloudReadStore{config: eruncommon.ERunConfig{
		CloudProviders: []eruncommon.CloudProviderConfig{{
			Alias:    gateRunClassifierE2EAlias,
			Provider: eruncommon.CloudProviderERun,
			ERun: &eruncommon.ERunProviderConfig{
				APIURL:   apiURL,
				ClientID: "dev-client",
			},
		}},
	}}
	deps := eruncommon.CloudDependencies{CloudSecretStore: secrets}
	return store, deps
}

// gateRunClassifierE2EBranchNames derives branch names from the running
// (sub)test's own name, which Go guarantees is unique within one test run --
// gate_runs has no uniqueness constraint on branch names, but distinct names
// keep each case's rows trivially distinguishable when inspecting the table
// by hand.
func gateRunClassifierE2EBranchNames(t *testing.T) (source, target string) {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	return "source-" + name, "target-" + name
}

// TestGateRunClassifierUpgradesKnownSignaturesToInconclusive drives every
// known infrastructure signature (erun-common's gateRunInconclusiveSignatures)
// through both RunGateRunStart (the "no trackable running phase" immediate
// outcome shape) and RunGateRunReport (start RUNNING, then report), against
// the real handler and a real Postgres, and confirms the persisted status --
// read back independently via RunGateRunShow -- is INCONCLUSIVE, never
// FAILED.
func TestGateRunClassifierUpgradesKnownSignaturesToInconclusive(t *testing.T) {
	config := gateRunClassifierE2EFromEnv(t)
	apiURL := startGateRunClassifierAPI(t, config)
	store, deps := gateRunClassifierE2EDeps(t, apiURL)
	ctx := eruncommon.Context{}

	signatures := eruncommon.GateRunInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("no known infrastructure signatures registered -- nothing to verify")
	}

	for _, signature := range signatures {
		t.Run(signature, func(t *testing.T) {
			source, target := gateRunClassifierE2EBranchNames(t)
			failingStep := "erun build (base image pull): " + signature

			// Shape 1: Start reports the immediate (terminal) outcome
			// directly, as a caller with no trackable running phase would.
			started, err := eruncommon.RunGateRunStart(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunStartParams{
				SourceBranch: source,
				TargetBranch: target,
				SourceCommit: "deadbeef",
				Status:       "failed",
				FailingStep:  failingStep,
			}, deps)
			mustNoErr(t, err, "RunGateRunStart")
			if started.Status != "INCONCLUSIVE" {
				t.Fatalf("RunGateRunStart returned status %q for signature %q, want INCONCLUSIVE", started.Status, signature)
			}
			readBack, err := eruncommon.RunGateRunShow(ctx, store, gateRunClassifierE2EAlias, started.GateRunID, deps)
			mustNoErr(t, err, "RunGateRunShow after Start")
			if readBack.Status != "INCONCLUSIVE" {
				t.Fatalf("persisted status for %s = %q, want INCONCLUSIVE (Start path)", started.GateRunID, readBack.Status)
			}

			// Shape 2: Start RUNNING, then Report the same signature via a
			// separate PATCH -- the shape a real merge-queue drive actually
			// uses (build the working tree, start the gate run, run `erun
			// build`, then report whatever it produced).
			running, err := eruncommon.RunGateRunStart(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunStartParams{
				SourceBranch: source,
				TargetBranch: target,
				SourceCommit: "deadbeef",
				MergeCommit:  "cafef00d",
			}, deps)
			mustNoErr(t, err, "RunGateRunStart (running)")
			if running.Status != "RUNNING" {
				t.Fatalf("RunGateRunStart with no status returned %q, want RUNNING", running.Status)
			}
			reported, err := eruncommon.RunGateRunReport(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunReportParams{
				GateRunID:   running.GateRunID,
				Status:      "failed",
				FailingStep: failingStep,
			}, deps)
			mustNoErr(t, err, "RunGateRunReport")
			if reported.Status != "INCONCLUSIVE" {
				t.Fatalf("RunGateRunReport returned status %q for signature %q, want INCONCLUSIVE", reported.Status, signature)
			}
			readBack, err = eruncommon.RunGateRunShow(ctx, store, gateRunClassifierE2EAlias, running.GateRunID, deps)
			mustNoErr(t, err, "RunGateRunShow after Report")
			if readBack.Status != "INCONCLUSIVE" {
				t.Fatalf("persisted status for %s = %q, want INCONCLUSIVE (Report path)", running.GateRunID, readBack.Status)
			}
		})
	}
}

// TestGateRunClassifierLeavesGenuineFailuresFailed is the other half of the
// property: a real test failure -- text that names no known infrastructure
// signature -- must persist as FAILED, never get laundered into
// INCONCLUSIVE. A classifier that upgrades everything would pass the
// previous test and still be strictly worse than not classifying at all.
func TestGateRunClassifierLeavesGenuineFailuresFailed(t *testing.T) {
	config := gateRunClassifierE2EFromEnv(t)
	apiURL := startGateRunClassifierAPI(t, config)
	store, deps := gateRunClassifierE2EDeps(t, apiURL)
	ctx := eruncommon.Context{}
	source, target := gateRunClassifierE2EBranchNames(t)

	running, err := eruncommon.RunGateRunStart(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunStartParams{
		SourceBranch: source,
		TargetBranch: target,
		SourceCommit: "deadbeef",
		MergeCommit:  "cafef00d",
	}, deps)
	mustNoErr(t, err, "RunGateRunStart")

	reported, err := eruncommon.RunGateRunReport(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunReportParams{
		GateRunID:   running.GateRunID,
		Status:      "failed",
		FailingStep: "go test ./erun-common/... : TestRunGateRunReport failed: assertion mismatch",
	}, deps)
	mustNoErr(t, err, "RunGateRunReport")
	if reported.Status != "FAILED" {
		t.Fatalf("RunGateRunReport returned status %q for a genuine failure, want FAILED", reported.Status)
	}

	readBack, err := eruncommon.RunGateRunShow(ctx, store, gateRunClassifierE2EAlias, running.GateRunID, deps)
	mustNoErr(t, err, "RunGateRunShow")
	if readBack.Status != "FAILED" {
		t.Fatalf("persisted status = %q, want FAILED", readBack.Status)
	}
}

// TestGateRunClassifierCallerCanDistinguishInconclusiveFromFailed proves the
// property callers actually depend on: given one INCONCLUSIVE run (a known
// signature) and one FAILED run (a genuine failure) on the same target
// branch, RunGateRunList -- the same call `erun gate list` and the MCP
// gate_list tool make -- reports them as two different statuses, and a
// status filter (what an operator or an automated caller would use to treat
// "did the gate produce a real verdict" as a yes/no question) includes one
// and excludes the other.
func TestGateRunClassifierCallerCanDistinguishInconclusiveFromFailed(t *testing.T) {
	config := gateRunClassifierE2EFromEnv(t)
	apiURL := startGateRunClassifierAPI(t, config)
	store, deps := gateRunClassifierE2EDeps(t, apiURL)
	ctx := eruncommon.Context{}
	source, target := gateRunClassifierE2EBranchNames(t)

	inconclusiveRun, err := eruncommon.RunGateRunStart(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunStartParams{
		SourceBranch: source + "-a",
		TargetBranch: target,
		SourceCommit: "deadbeef1",
		Status:       "failed",
		FailingStep:  "erun build: " + eruncommon.GateRunInconclusiveSignatures()[0],
	}, deps)
	mustNoErr(t, err, "RunGateRunStart (known signature)")
	if inconclusiveRun.Status != "INCONCLUSIVE" {
		t.Fatalf("setup: known-signature run status = %q, want INCONCLUSIVE", inconclusiveRun.Status)
	}

	failedRun, err := eruncommon.RunGateRunStart(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunStartParams{
		SourceBranch: source + "-b",
		TargetBranch: target,
		SourceCommit: "deadbeef2",
		Status:       "failed",
		FailingStep:  "go vet ./...: exit status 1",
	}, deps)
	mustNoErr(t, err, "RunGateRunStart (genuine failure)")
	if failedRun.Status != "FAILED" {
		t.Fatalf("setup: genuine-failure run status = %q, want FAILED", failedRun.Status)
	}

	all, err := eruncommon.RunGateRunList(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunListParams{TargetBranch: target}, deps)
	mustNoErr(t, err, "RunGateRunList")
	statuses := map[string]string{}
	for _, run := range all {
		statuses[run.GateRunID] = run.Status
	}
	if statuses[inconclusiveRun.GateRunID] != "INCONCLUSIVE" {
		t.Fatalf("gate list status for the known-signature run = %q, want INCONCLUSIVE", statuses[inconclusiveRun.GateRunID])
	}
	if statuses[failedRun.GateRunID] != "FAILED" {
		t.Fatalf("gate list status for the genuine-failure run = %q, want FAILED", statuses[failedRun.GateRunID])
	}

	failedOnly, err := eruncommon.RunGateRunList(ctx, store, gateRunClassifierE2EAlias, eruncommon.GateRunListParams{TargetBranch: target, Status: "failed"}, deps)
	mustNoErr(t, err, "RunGateRunList status=failed")
	if gateRunListContainsID(failedOnly, inconclusiveRun.GateRunID) {
		t.Fatalf("status=failed filter returned the known-signature run %s, which is INCONCLUSIVE -- a caller filtering on FAILED would wrongly treat it as red", inconclusiveRun.GateRunID)
	}
	if !gateRunListContainsID(failedOnly, failedRun.GateRunID) {
		t.Fatalf("status=failed filter did not return the genuine failure %s", failedRun.GateRunID)
	}
}

func gateRunListContainsID(runs []eruncommon.PlatformGateRun, gateRunID string) bool {
	for _, run := range runs {
		if run.GateRunID == gateRunID {
			return true
		}
	}
	return false
}

// TestReviewRecordBuildRefusesKnownSignatureGateFailure exercises the
// review-status half of the fix: RunReviewRecordBuild --gate --failed
// refuses outright (never records a FAILED build, never touches the
// network -- proven here by passing a nil store/deps that would panic or
// error differently if execution ever reached alias resolution) when
// --failure-detail matches a known infrastructure signature, so the
// review's own status cannot disagree with the gate run's classification
// for the same underlying event.
func TestReviewRecordBuildRefusesKnownSignatureGateFailure(t *testing.T) {
	ctx := eruncommon.Context{}
	signature := eruncommon.GateRunInconclusiveSignatures()[0]

	_, err := eruncommon.RunReviewRecordBuild(ctx, nil, "", eruncommon.ReviewRecordBuildParams{
		ReviewID:      "unused",
		CommitID:      "deadbeef",
		Gate:          true,
		Successful:    false,
		FailureDetail: "erun build: " + signature,
	}, eruncommon.CloudDependencies{})
	if err == nil {
		t.Fatal("RunReviewRecordBuild did not refuse a --gate --failed report matching a known infrastructure signature")
	}
	if !strings.Contains(err.Error(), "known erun infrastructure") {
		t.Fatalf("RunReviewRecordBuild's error does not name the classifier refusal (got %q) -- it may have failed for an unrelated reason, e.g. reaching alias resolution with a nil store", err.Error())
	}

	// The genuine-failure counterpart must NOT be refused -- a real defect
	// gets recorded as a real FAILED build, not silently swallowed.
	_, err = eruncommon.RunReviewRecordBuild(ctx, nil, "", eruncommon.ReviewRecordBuildParams{
		ReviewID:      "unused",
		CommitID:      "deadbeef",
		Gate:          true,
		Successful:    false,
		FailureDetail: "go vet ./...: exit status 1",
	}, eruncommon.CloudDependencies{})
	if err == nil || strings.Contains(err.Error(), "known erun infrastructure") {
		t.Fatalf("RunReviewRecordBuild refused a genuine failure as if it matched a known infrastructure signature: %v", err)
	}
	if !errors.Is(err, eruncommon.ErrPlatformAliasUnusable) {
		t.Fatalf("expected the genuine failure to fall through to alias resolution (nil store, no alias configured) and fail there, got: %v", err)
	}
}
