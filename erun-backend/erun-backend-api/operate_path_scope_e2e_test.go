package backendapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// The operate path — the reads and writes a deploy, stop, delete, or job
// report drives — used to key every row on an id alone. That is only safe
// while RLS is the enforcement: contexts, context_credentials, environments,
// jobs, builds, and gate_runs all carry
// `FOR ALL TO erun_operations USING (true)`, so an OPERATIONS caller naming a
// stranger tenant's id was answered with that stranger's row by every one of
// them. These tests pin the property against a real migrated PostgreSQL and
// the real erun_operations policy, because that is the only venue in which the
// difference between "the SQL scopes it" and "RLS would have scoped it" is
// observable at all.

// operatePathCipher builds a real AES-256-GCM cipher, so the credential tests
// exercise the actual encrypt-then-custody-then-decrypt path rather than a
// stand-in that would agree with whatever it was handed.
func operatePathCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32)))
	mustNoErr(t, err, "new cipher")
	return cipher
}

// TestContextCredentialReadIsScopedToTheOwningTenant is the highest-payload
// member of this class: naming a context id alone released that context's k3s
// admin token. The token is written into a placement Secret and handed to a
// Job that runs kubectl against the cluster it authenticates — so the read
// itself is the release, whether or not whatever follows it succeeds.
func TestContextCredentialReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	contexts := repository.NewContextRepository(txs)
	credentials := repository.NewContextCredentialRepository(txs, operatePathCipher(t))

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-cluster", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	mustNoErr(t, credentials.Set(strangerCtx, strangerContext.ContextID, "stranger-admin-token"), "custody stranger token")

	// The reputation of the fix: an OPERATIONS caller naming only the
	// stranger's context id must not be handed the stranger's token, even
	// though erun_operations' policy makes the row visible to it.
	if _, err := credentials.Get(opsCtx, opsTenantID, strangerContext.ContextID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("credential read for a context the caller's tenant does not own = %v, want ErrNotFound", err)
	}

	// The owning tenant still gets its own token: scoping must not turn into
	// refusing the legitimate read.
	token, err := credentials.Get(opsCtx, strangerTenantID, strangerContext.ContextID)
	mustNoErr(t, err, "read the token on behalf of its owner")
	if token != "stranger-admin-token" {
		t.Fatalf("token = %q, want stranger-admin-token", token)
	}
}

// TestContextReadIsScopedToTheOwningTenant covers the coordinate read every
// placement resolution starts from — project this id, project this server URL,
// project this kubernetes context — which is what the credential above is
// looked up alongside.
func TestContextReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	contexts := repository.NewContextRepository(repository.NewTxManager(db, repository.DialectPostgres))

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-cluster", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	mustNoErr(t, contexts.UpdateProvisioningResult(strangerCtx, strangerContext.ContextID, "running", "i-1234", "203.0.113.10", ""), "record stranger cluster coordinates")

	if _, err := contexts.Get(opsCtx, opsTenantID, strangerContext.ContextID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("context read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	got, err := contexts.Get(opsCtx, strangerTenantID, strangerContext.ContextID)
	mustNoErr(t, err, "read the context on behalf of its owner")
	if got.ContextID != strangerContext.ContextID || got.PublicIP != "203.0.113.10" {
		t.Fatalf("context = %+v, want the tenant's own row", got)
	}
}

// seedOperatePathRows creates one row of each operate-path class for the
// tenant ctx names, so the scoping below is proven per class against real rows
// rather than one id reused across them.
func seedOperatePathRows(t *testing.T, db *sql.DB, ctx context.Context, txs *repository.TxManager) (model.Job, model.Review, model.Build, model.GateRun) {
	t.Helper()
	jobs := repository.NewJobRepository(txs)
	reviews := repository.NewReviewRepository(txs)
	builds := repository.NewBuildRepository(txs)
	gateRuns := repository.NewGateRunRepository(txs)

	// A review's author_user_id is NOT NULL and database-defaulted from
	// erun_current_user_id(), which reads the erun.user_id the transaction
	// wiring sets from the security context. A context naming a tenant but no
	// user therefore cannot create a review at all, so the seeder plants one
	// author per tenant and carries it — the same shape reviews_e2e_test.go
	// uses.
	authorCtx := operatePathAuthorContext(t, db, ctx)

	job, err := service.NewJobService(jobs).Claim(ctx, model.Job{
		JobType:   model.JobTypeFix,
		Summary:   "the tenant's in-flight work",
		ActorKind: model.ActorKindAgent,
		ActorID:   "operate-path-coder",
	})
	mustNoErr(t, err, "claim a job for the tenant")

	review, err := reviews.Create(authorCtx, model.Review{
		Name:         "the tenant's proposal",
		TargetBranch: "main",
		SourceBranch: "feature/operate-path",
		Status:       model.ReviewStatusOpen,
	})
	mustNoErr(t, err, "create the tenant's review")

	build, err := builds.Create(ctx, model.Build{
		ReviewID:   review.ReviewID,
		Kind:       model.BuildKindRecorded,
		Successful: true,
		CommitID:   "operate-path-commit",
		Version:    "1.0.0",
	})
	mustNoErr(t, err, "create the tenant's build")

	gateRun, err := gateRuns.Create(ctx, model.GateRun{
		SourceBranch: "feature/operate-path",
		TargetBranch: "main",
		SourceCommit: "operate-path-source-sha",
		// gate_runs_merge_commit_required_check: only a FAILED or
		// INCONCLUSIVE run may carry no merge commit.
		MergeCommit: "operate-path-merge-sha",
		Status:      model.GateRunStatusRunning,
	})
	mustNoErr(t, err, "create the tenant's gate run")

	return job, review, build, gateRun
}

// operatePathAuthorSeq makes each seeded author's username unique: users is
// unique per (tenant, username), and several tests seed more than one tenant.
var operatePathAuthorSeq int

// operatePathAuthorContext plants a user in ctx's tenant and returns ctx
// carrying it, so a write whose column defaults to erun_current_user_id() has
// an author to record.
func operatePathAuthorContext(t *testing.T, db *sql.DB, ctx context.Context) context.Context {
	t.Helper()
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		t.Fatal("operatePathAuthorContext needs a tenant-scoped context")
	}
	operatePathAuthorSeq++
	var userID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO users (tenant_id, username) VALUES ($1, $2) RETURNING user_id`,
		securityContext.TenantID, fmt.Sprintf("operate-path-author-%d", operatePathAuthorSeq),
	).Scan(&userID), "seed operate-path author")

	securityContext.ErunUserID = userID
	return security.WithContext(ctx, securityContext)
}

// TestJobReadAndUpdateAreScopedToTheOwningTenant covers the job routes' own
// class: GET and PATCH /v1/jobs/{job_id} both keyed on job_id alone, which for
// an OPERATIONS caller — whose RLS policy is unconditional — resolved to any
// tenant's row.
func TestJobReadAndUpdateAreScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	jobs := repository.NewJobRepository(repository.NewTxManager(db, repository.DialectPostgres))
	strangerJob, _, _, _ := seedOperatePathRows(t, db, strangerCtx, repository.NewTxManager(db, repository.DialectPostgres))

	if _, err := jobs.Get(opsCtx, opsTenantID, strangerJob.JobID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("job read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	// The owner still gets its own row: scoping must not turn into refusing
	// the legitimate read.
	owned, err := jobs.Get(strangerCtx, strangerTenantID, strangerJob.JobID)
	mustNoErr(t, err, "read the stranger's job on behalf of its owner")
	if owned.JobID != strangerJob.JobID {
		t.Fatalf("job read on behalf of its owner = %q, want %q", owned.JobID, strangerJob.JobID)
	}

	// The write carries the same scope, so a caller naming another tenant's
	// job moves nothing.
	ended := time.Now().UTC()
	strangerJob.Status = model.JobStatusSucceeded
	strangerJob.EndedAt = &ended
	if _, err := jobs.Update(opsCtx, opsTenantID, strangerJob); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("job update for a tenant that does not own it = %v, want ErrNotFound", err)
	}
	untouched, err := jobs.Get(strangerCtx, strangerTenantID, strangerJob.JobID)
	mustNoErr(t, err, "read the stranger's job back")
	if untouched.Status != model.JobStatusRunning {
		t.Fatalf("the stranger's job status = %q after a cross-tenant update, want it untouched at RUNNING", untouched.Status)
	}
	if _, err := jobs.Update(strangerCtx, strangerTenantID, strangerJob); err != nil {
		t.Fatalf("job update on behalf of its owner = %v, want it to succeed", err)
	}
}

// TestBuildReadIsScopedToTheOwningTenant covers the nested build route, whose
// query joined reviews on tenant only to read the review name back — the left
// half was never a filter, and the {review_id} in the path reached no SQL at
// all, so the build id was the whole of the lookup.
func TestBuildReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	builds := repository.NewBuildRepository(txs)
	_, _, strangerBuild, _ := seedOperatePathRows(t, db, strangerCtx, txs)

	if _, err := builds.Get(opsCtx, opsTenantID, strangerBuild.BuildID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("build read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	owned, err := builds.Get(strangerCtx, strangerTenantID, strangerBuild.BuildID)
	mustNoErr(t, err, "read the stranger's build on behalf of its owner")
	if owned.BuildID != strangerBuild.BuildID || owned.CommitID != "operate-path-commit" {
		t.Fatalf("build read on behalf of its owner = %+v, want the tenant's own row", owned)
	}
}

// TestGateRunReadAndUpdateAreScopedToTheOwningTenant covers GET and PATCH
// /v1/gate-runs/{gate_run_id}, whose LEFT JOIN decorates the row with a review
// name but scopes nothing.
func TestGateRunReadAndUpdateAreScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	gateRuns := repository.NewGateRunRepository(repository.NewTxManager(db, repository.DialectPostgres))
	_, _, _, strangerRun := seedOperatePathRows(t, db, strangerCtx, repository.NewTxManager(db, repository.DialectPostgres))

	if _, err := gateRuns.Get(opsCtx, opsTenantID, strangerRun.GateRunID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("gate run read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	owned, err := gateRuns.Get(strangerCtx, strangerTenantID, strangerRun.GateRunID)
	mustNoErr(t, err, "read the stranger's gate run on behalf of its owner")
	if owned.GateRunID != strangerRun.GateRunID {
		t.Fatalf("gate run read on behalf of its owner = %q, want %q", owned.GateRunID, strangerRun.GateRunID)
	}

	// A verdict is the write that route performs, so it is the one that must
	// not be reachable across tenants.
	strangerRun.Status = model.GateRunStatusPassed
	if _, err := gateRuns.Update(opsCtx, opsTenantID, strangerRun); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("gate run update for a tenant that does not own it = %v, want ErrNotFound", err)
	}
	untouched, err := gateRuns.Get(strangerCtx, strangerTenantID, strangerRun.GateRunID)
	mustNoErr(t, err, "read the stranger's gate run back")
	if untouched.Status != model.GateRunStatusRunning {
		t.Fatalf("the stranger's gate run status = %q after a cross-tenant verdict, want it untouched at RUNNING", untouched.Status)
	}
	if _, err := gateRuns.Update(strangerCtx, strangerTenantID, strangerRun); err != nil {
		t.Fatalf("gate run update on behalf of its owner = %v, want it to succeed", err)
	}
}

// TestEnvironmentMutatorsAreScopedToTheOwningTenant covers the six environment
// mutating methods the operate routes, the delete reconciler, and the delete
// lifecycle all reach. Their safety used to be inherited rather than stated:
// every caller fed them an id it had obtained from the scoped Get, so nothing
// in the methods themselves named a tenant, and the next caller with an id and
// no scoped Get would have re-opened the hole with no test to catch it.
func TestEnvironmentMutatorsAreScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	environments := repository.NewEnvironmentRepository(repository.NewTxManager(db, repository.DialectPostgres))

	strangerEnv, err := environments.Create(strangerCtx, model.Environment{Name: "stranger-operate-env", Type: model.EnvironmentTypeRuntime, RuntimeVersion: "1.0.0"})
	mustNoErr(t, err, "create the stranger's environment")
	mustNoErr(t, environments.UpdateProvisioningStatus(strangerCtx, strangerTenantID, strangerEnv.EnvironmentID, repository.EnvironmentStatusUpdate{
		Status: string(model.EnvironmentStatusRunning),
	}), "mark the stranger's environment running")

	claimed, err := environments.ClaimDeploy(opsCtx, opsTenantID, strangerEnv.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim a deploy on a stranger's environment")
	if claimed {
		t.Fatal("ClaimDeploy claimed an environment outside the caller's tenant")
	}
	claimed, err = environments.ClaimDelete(opsCtx, opsTenantID, strangerEnv.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim a delete on a stranger's environment")
	if claimed {
		t.Fatal("ClaimDelete claimed an environment outside the caller's tenant")
	}

	// The unconditional writes report no error when they match no row, so the
	// property to assert is that the stranger's row is untouched — read back
	// as its owner — rather than that the call returned an error it never had.
	mustNoErr(t, environments.UpdateProvisioningStatus(opsCtx, opsTenantID, strangerEnv.EnvironmentID, repository.EnvironmentStatusUpdate{
		Status: string(model.EnvironmentStatusFailed), ProvisionError: "cross-tenant status write",
	}), "cross-tenant status write")
	mustNoErr(t, environments.MarkDeployFailed(opsCtx, opsTenantID, strangerEnv.EnvironmentID, "cross-tenant deploy failure"), "cross-tenant mark-deploy-failed")
	mustNoErr(t, environments.MarkDeleteBlocked(opsCtx, opsTenantID, strangerEnv.EnvironmentID, "cross-tenant blocker"), "cross-tenant mark-delete-blocked")
	mustNoErr(t, environments.Delete(opsCtx, opsTenantID, strangerEnv.EnvironmentID), "cross-tenant delete")

	untouched, err := environments.Get(strangerCtx, strangerEnv.EnvironmentID)
	mustNoErr(t, err, "the stranger's environment must still exist after a cross-tenant delete")
	if untouched.Status != model.EnvironmentStatusRunning || untouched.ProvisionError != "" || untouched.DeleteError != "" {
		t.Fatalf("the stranger's environment after cross-tenant writes = status %q provisionError %q deleteError %q, want it untouched at RUNNING with neither error",
			untouched.Status, untouched.ProvisionError, untouched.DeleteError)
	}

	// The owner's own writes still land: scoping must not have broken the
	// reconciler's and the lifecycle's legitimate paths.
	claimed, err = environments.ClaimDelete(strangerCtx, strangerTenantID, strangerEnv.EnvironmentID, time.Hour)
	mustNoErr(t, err, "claim a delete on behalf of the owner")
	if !claimed {
		t.Fatal("ClaimDelete on behalf of the owning tenant was refused")
	}
	mustNoErr(t, environments.MarkDeleteBlocked(strangerCtx, strangerTenantID, strangerEnv.EnvironmentID, "namespace stuck"), "mark delete blocked on behalf of the owner")
	blocked, err := environments.Get(strangerCtx, strangerEnv.EnvironmentID)
	mustNoErr(t, err, "read the environment back")
	if blocked.Status != model.EnvironmentStatusDeletionBlocked || blocked.DeleteError != "namespace stuck" {
		t.Fatalf("the owner's own delete-blocked write = status %q deleteError %q, want DELETION_BLOCKED with its reason", blocked.Status, blocked.DeleteError)
	}
	mustNoErr(t, environments.Delete(strangerCtx, strangerTenantID, strangerEnv.EnvironmentID), "delete on behalf of the owner")
	if _, err := environments.Get(strangerCtx, strangerEnv.EnvironmentID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("the owner's own delete = %v, want the row gone (ErrNotFound)", err)
	}
}

// TestOperateReadsResolveTheCallersOwnRows is the control for the comparison
// below: without it, a route that had stopped resolving anything at all would
// satisfy "a cross-tenant id answers what a missing id answers" by answering
// 404 to everything.
func TestOperateReadsResolveTheCallersOwnRows(t *testing.T) {
	opsCtx, _, opsTenantID, _, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	ownJob, ownReview, ownBuild, ownRun := seedOperatePathRows(t, db, opsCtx, txs)

	opsUserID := seedScopeTestUser(t, db, opsTenantID, "ops-caller")
	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)

	for _, tc := range []struct {
		label  string
		method string
		path   string
	}{
		{"GET /v1/jobs/{job_id}", http.MethodGet, "/v1/jobs/" + ownJob.JobID},
		{"GET /v1/reviews/{review_id}/builds/{build_id}", http.MethodGet, "/v1/reviews/" + ownReview.ReviewID + "/builds/" + ownBuild.BuildID},
		{"GET /v1/gate-runs/{gate_run_id}", http.MethodGet, "/v1/gate-runs/" + ownRun.GateRunID},
	} {
		code, body := e2eRequest(t, opsServer.URL, tc.method, tc.path, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: the caller's own row answered HTTP %d (want 200): %s", tc.label, code, body)
		}
	}
}

// TestCrossTenantOperateReadsAreIndistinguishableFromMissingOnes is the
// acceptance property stated as a comparison rather than two separate
// assertions: for every operate read and write, a valid id naming another
// tenant's row must answer exactly what a valid id naming nothing answers.
// Anything that told the two apart — a 403 where a missing id gives 404, a
// different error code, a response body naming the row — would confirm the row
// exists, which is the thing the scoping is for.
//
// The caller's own rows are read successfully alongside, so a route that had
// simply stopped working could not satisfy the comparison by answering 404 to
// everything.
func TestCrossTenantOperateReadsAreIndistinguishableFromMissingOnes(t *testing.T) {
	_, strangerCtx, opsTenantID, _, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	strangerJob, strangerReview, strangerBuild, strangerRun := seedOperatePathRows(t, db, strangerCtx, txs)

	opsUserID := seedScopeTestUser(t, db, opsTenantID, "ops-caller")
	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)

	for _, tc := range []struct {
		label       string
		method      string
		crossTenant string
		missing     string
		body        map[string]any
	}{
		{
			label:       "GET /v1/jobs/{job_id}",
			method:      http.MethodGet,
			crossTenant: "/v1/jobs/" + strangerJob.JobID,
			missing:     "/v1/jobs/" + uuid.NewString(),
		},
		{
			label:       "PATCH /v1/jobs/{job_id}",
			method:      http.MethodPatch,
			crossTenant: "/v1/jobs/" + strangerJob.JobID,
			missing:     "/v1/jobs/" + uuid.NewString(),
			body:        map[string]any{"summary": "a cross-tenant write"},
		},
		{
			label:       "GET /v1/reviews/{review_id}/builds/{build_id}",
			method:      http.MethodGet,
			crossTenant: "/v1/reviews/" + strangerReview.ReviewID + "/builds/" + strangerBuild.BuildID,
			missing:     "/v1/reviews/" + strangerReview.ReviewID + "/builds/" + uuid.NewString(),
		},
		{
			label:       "GET /v1/gate-runs/{gate_run_id}",
			method:      http.MethodGet,
			crossTenant: "/v1/gate-runs/" + strangerRun.GateRunID,
			missing:     "/v1/gate-runs/" + uuid.NewString(),
		},
		{
			label:       "PATCH /v1/gate-runs/{gate_run_id}",
			method:      http.MethodPatch,
			crossTenant: "/v1/gate-runs/" + strangerRun.GateRunID,
			missing:     "/v1/gate-runs/" + uuid.NewString(),
			body:        map[string]any{"status": "FAILED", "failingStep": "erun build"},
		},
	} {
		crossCode, crossBody := e2eRequest(t, opsServer.URL, tc.method, tc.crossTenant, tc.body)
		missingCode, missingBody := e2eRequest(t, opsServer.URL, tc.method, tc.missing, tc.body)

		if crossCode != http.StatusNotFound || missingCode != http.StatusNotFound {
			t.Fatalf("%s: a cross-tenant id answered HTTP %d and a missing id answered HTTP %d, want 404 for both: %s / %s",
				tc.label, crossCode, missingCode, crossBody, missingBody)
		}
		if crossCode != missingCode || crossBody != missingBody {
			t.Fatalf("%s: a cross-tenant id is distinguishable from a genuinely missing one: cross-tenant = HTTP %d %s, missing = HTTP %d %s",
				tc.label, crossCode, crossBody, missingCode, missingBody)
		}
	}

	// Nothing the cross-tenant calls reached wrote anything.
	untouchedJob, err := repository.NewJobRepository(txs).Get(strangerCtx, strangerJob.TenantID, strangerJob.JobID)
	mustNoErr(t, err, "read the stranger's job back")
	if untouchedJob.Status != model.JobStatusRunning || untouchedJob.Summary != strangerJob.Summary {
		t.Fatalf("the stranger's job after cross-tenant writes = status %q summary %q, want it untouched", untouchedJob.Status, untouchedJob.Summary)
	}
	untouchedRun, err := repository.NewGateRunRepository(txs).Get(strangerCtx, strangerRun.TenantID, strangerRun.GateRunID)
	mustNoErr(t, err, "read the stranger's gate run back")
	if untouchedRun.Status != model.GateRunStatusRunning {
		t.Fatalf("the stranger's gate run after a cross-tenant verdict = %q, want it untouched at RUNNING", untouchedRun.Status)
	}
}
