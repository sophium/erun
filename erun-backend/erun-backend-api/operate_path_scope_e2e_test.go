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

// erun_operations RLS is USING (true) on every operate-path table, so the SQL itself
// must scope by tenant; only real PostgreSQL makes that observable.

func operatePathCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32)))
	mustNoErr(t, err, "new cipher")
	return cipher
}

// TestContextCredentialReadIsScopedToTheOwningTenant: reading the credential is itself the release
// of the context's k3s admin token.
func TestContextCredentialReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	contexts := repository.NewContextRepository(txs)
	credentials := repository.NewContextCredentialRepository(txs, operatePathCipher(t))

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-cluster", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	mustNoErr(t, credentials.Set(strangerCtx, strangerContext.ContextID, "stranger-admin-token"), "custody stranger token")

	if _, err := credentials.Get(opsCtx, opsTenantID, strangerContext.ContextID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("credential read for a context the caller's tenant does not own = %v, want ErrNotFound", err)
	}

	token, err := credentials.Get(opsCtx, strangerTenantID, strangerContext.ContextID)
	mustNoErr(t, err, "read the token on behalf of its owner")
	if token != "stranger-admin-token" {
		t.Fatalf("token = %q, want stranger-admin-token", token)
	}
}

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

func seedOperatePathRows(t *testing.T, db *sql.DB, ctx context.Context, txs *repository.TxManager) (model.Job, model.Review, model.Build) {
	t.Helper()
	jobs := repository.NewJobRepository(txs)
	reviews := repository.NewReviewRepository(txs)
	builds := repository.NewBuildRepository(txs)

	// author_user_id defaults from erun_current_user_id(), so reviews need a user in context.
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

	return job, review, build
}

// operatePathAuthorSeq keeps usernames unique per (tenant, username).
var operatePathAuthorSeq int

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

func TestJobReadAndUpdateAreScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	jobs := repository.NewJobRepository(repository.NewTxManager(db, repository.DialectPostgres))
	strangerJob, _, _ := seedOperatePathRows(t, db, strangerCtx, repository.NewTxManager(db, repository.DialectPostgres))

	if _, err := jobs.Get(opsCtx, opsTenantID, strangerJob.JobID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("job read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	owned, err := jobs.Get(strangerCtx, strangerTenantID, strangerJob.JobID)
	mustNoErr(t, err, "read the stranger's job on behalf of its owner")
	if owned.JobID != strangerJob.JobID {
		t.Fatalf("job read on behalf of its owner = %q, want %q", owned.JobID, strangerJob.JobID)
	}

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

// TestBuildReadIsScopedToTheOwningTenant: the {review_id} path segment must scope the lookup, not
// the build id alone.
func TestBuildReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	builds := repository.NewBuildRepository(txs)
	_, _, strangerBuild := seedOperatePathRows(t, db, strangerCtx, txs)

	if _, err := builds.Get(opsCtx, opsTenantID, strangerBuild.BuildID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("build read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	owned, err := builds.Get(strangerCtx, strangerTenantID, strangerBuild.BuildID)
	mustNoErr(t, err, "read the stranger's build on behalf of its owner")
	if owned.BuildID != strangerBuild.BuildID || owned.CommitID != "operate-path-commit" {
		t.Fatalf("build read on behalf of its owner = %+v, want the tenant's own row", owned)
	}
}

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

	// Unconditional writes report no error on zero rows, so assert the row is untouched.
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

// TestOperateReadsResolveTheCallersOwnRows is the control: without it, answering 404 to
// everything would pass the comparison below.
func TestOperateReadsResolveTheCallersOwnRows(t *testing.T) {
	opsCtx, _, opsTenantID, _, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	ownJob, ownReview, ownBuild := seedOperatePathRows(t, db, opsCtx, txs)

	opsUserID := seedScopeTestUser(t, db, opsTenantID, "ops-caller")
	opsServer := startEnvironmentsAPIServer(t, db, opsTenantID, model.TenantTypeOperations, opsUserID)

	for _, tc := range []struct {
		label  string
		method string
		path   string
	}{
		{"GET /v1/jobs/{job_id}", http.MethodGet, "/v1/jobs/" + ownJob.JobID},
		{"GET /v1/reviews/{review_id}/builds/{build_id}", http.MethodGet, "/v1/reviews/" + ownReview.ReviewID + "/builds/" + ownBuild.BuildID},
	} {
		code, body := e2eRequest(t, opsServer.URL, tc.method, tc.path, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: the caller's own row answered HTTP %d (want 200): %s", tc.label, code, body)
		}
	}
}

// TestCrossTenantOperateReadsAreIndistinguishableFromMissingOnes: a cross-tenant id must answer
// exactly what a missing id answers; any difference confirms the row exists.
func TestCrossTenantOperateReadsAreIndistinguishableFromMissingOnes(t *testing.T) {
	_, strangerCtx, opsTenantID, _, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	strangerJob, strangerReview, strangerBuild := seedOperatePathRows(t, db, strangerCtx, txs)

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

	untouchedJob, err := repository.NewJobRepository(txs).Get(strangerCtx, strangerJob.TenantID, strangerJob.JobID)
	mustNoErr(t, err, "read the stranger's job back")
	if untouchedJob.Status != model.JobStatusRunning || untouchedJob.Summary != strangerJob.Summary {
		t.Fatalf("the stranger's job after cross-tenant writes = status %q summary %q, want it untouched", untouchedJob.Status, untouchedJob.Summary)
	}
}
