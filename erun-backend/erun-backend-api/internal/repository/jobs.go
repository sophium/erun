package repository

import (
	"context"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const jobColumns = `job_id, tenant_id, environment_id, job_type, issue_ref, summary, status,
	actor_kind, actor_id, scope, local_job_id, started_at, ended_at, created_at, updated_at`

type JobRepository struct {
	txs *TxManager
}

// JobFilter composes GET /v1/jobs discovery filters. Every field is optional
// and AND-ed together; an empty filter lists every job visible to the
// caller's tenant.
type JobFilter struct {
	Status        model.JobStatus
	EnvironmentID string
	IssueRef      string
	Scope         string
	ActorID       string
}

func NewJobRepository(txs *TxManager) *JobRepository {
	return &JobRepository{txs: txs}
}

func (r *JobRepository) Create(ctx context.Context, job model.Job) (model.Job, error) {
	created := job
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("environment_id", "job_type", "issue_ref", "summary", "status",
				"actor_kind", "actor_id", "scope", "local_job_id", "started_at", "ended_at").
			Returning("*").
			Scan(ctx)
		return classifyJobError(err)
	})
	return created, err
}

// classifyJobError maps jobs' foreign key and CHECK constraints onto the
// repository's sentinel errors, mirroring classifyGateRunError: an
// environmentId the caller's tenant cannot see fails the same foreign key
// check whether it genuinely doesn't exist or just isn't this tenant's.
func classifyJobError(err error) error {
	code, ok := pgErrorCode(err)
	if !ok {
		return err
	}
	switch code {
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalidInput
	default:
		return err
	}
}

// Get returns a job owned by tenantID, or ErrNotFound for an id that names
// another tenant's job. The tenant is an explicit predicate and not left to
// RLS, the same reason EnvironmentRepository.Get states it: erun_operations'
// RLS policy is unconditional (USING (true)), so for an OPERATIONS caller an
// id-only lookup would answer with a stranger's row — here on the job routes,
// where Get's result is what PATCH /v1/jobs/{job_id} then moves forward.
func (r *JobRepository) Get(ctx context.Context, tenantID, jobID string) (model.Job, error) {
	var job model.Job
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+jobColumns+`
			  FROM jobs
			 WHERE job_id = ?
			   AND tenant_id = ?
		`, jobID, tenantID).Scan(ctx, &job)
		return normalizeNoRows(err)
	})
	return job, err
}

// List returns the caller's tenant's jobs narrowed by filter, the live queue
// first: every RUNNING job ahead of the finished ones, then most recently
// started first. Scoped explicitly by tenant_id from the security context
// rather than left to RLS: erun_operations' policy is unconditional, so an
// OPERATIONS caller's empty filter would otherwise read every tenant's jobs.
func (r *JobRepository) List(ctx context.Context, filter JobFilter) ([]model.Job, error) {
	return r.list(ctx, filter, "")
}

// ListByEnvironment is List narrowed to one environment — the per-environment
// view, which mirrors the ai-sessions sub-route's own read-back for a caller
// watching a single environment rather than the whole tenant queue.
func (r *JobRepository) ListByEnvironment(ctx context.Context, environmentID string) ([]model.Job, error) {
	return r.list(ctx, JobFilter{}, environmentID)
}

func (r *JobRepository) list(ctx context.Context, filter JobFilter, environmentID string) ([]model.Job, error) {
	// An empty list is a definite "nothing is recorded", so it is built
	// non-nil and marshals as [] rather than null — a caller ranging over the
	// body must not need a null check.
	jobs := []model.Job{}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT ` + jobColumns + `
			  FROM jobs
			 WHERE tenant_id = ?
		`
		args := []any{securityContext.TenantID}
		if environmentID != "" {
			query += ` AND environment_id = ?`
			args = append(args, environmentID)
		}
		if filter.Status != "" {
			query += ` AND status = ?`
			args = append(args, filter.Status)
		}
		if filter.EnvironmentID != "" {
			query += ` AND environment_id = ?`
			args = append(args, filter.EnvironmentID)
		}
		if filter.IssueRef != "" {
			query += ` AND issue_ref = ?`
			args = append(args, filter.IssueRef)
		}
		if filter.Scope != "" {
			query += ` AND scope = ?`
			args = append(args, filter.Scope)
		}
		if filter.ActorID != "" {
			query += ` AND actor_id = ?`
			args = append(args, filter.ActorID)
		}
		query += ` ORDER BY (status = 'RUNNING') DESC, started_at DESC, job_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &jobs)
	})
	return jobs, err
}

// FindOpenByScope returns the job currently holding scope, if one is. The
// lookup is the claim primitive's read half: a caller about to start work
// asks this first so a second claim can be refused with the holder named,
// instead of two actors silently duplicating each other.
//
// A scope no open job holds is reported as ErrNotFound, the same sentinel a
// missing row resolves to, so a caller cannot confuse "nobody holds this"
// with a lookup that failed.
func (r *JobRepository) FindOpenByScope(ctx context.Context, scope string) (model.Job, error) {
	var job model.Job
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		err = tx.NewRaw(`
			SELECT `+jobColumns+`
			  FROM jobs
			 WHERE tenant_id = ?
			   AND scope = ?
			   AND status = 'RUNNING'
			 ORDER BY started_at DESC, job_id DESC
			 LIMIT 1
		`, securityContext.TenantID, scope).Scan(ctx, &job)
		return normalizeNoRows(err)
	})
	return job, err
}

// AbandonStale closes every RUNNING job whose last update predates
// staleBefore, returning the rows it closed so the sweep's effect is
// observable rather than inferred from a count.
//
// This is deliberately not tenant-scoped. A sweep runs under the operations
// role across every tenant at once — a job abandoned in one tenant must not
// require that tenant to ask for it to be cleared — and it is the only write
// in this repository that reads no tenant from the security context. The
// tenant's own reads stay scoped in SQL rather than left to RLS, because
// erun_operations' policy is unconditional (see List).
//
// One statement, not a read followed by a write: a row that a later update
// refreshes between the two would otherwise be abandoned on the strength of
// the state it had when it was read.
func (r *JobRepository) AbandonStale(ctx context.Context, staleBefore time.Time) ([]model.Job, error) {
	// A sweep that closed nothing is still a definite answer, so the slice is
	// built non-nil and marshals as [] rather than null -- the same contract
	// every list in this repository holds to.
	abandoned := []model.Job{}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			UPDATE jobs
			   SET status = 'ABANDONED', ended_at = NOW()
			 WHERE status = 'RUNNING'
			   AND updated_at < ?
			RETURNING `+jobColumns+`
		`, staleBefore).Scan(ctx, &abandoned)
	})
	return abandoned, err
}

// Update persists a job's progress: its status, summary and local_job_id,
// plus the ended_at the status change implies. Every other field — what the
// job claims, who holds it, when it started — is immutable after creation,
// so a job cannot be reassigned to another actor by a later write.
// Update writes tenantID's own job, carrying the same explicit tenant
// predicate Get does. The service reaches this only with a job it read back
// through Get under the same tenant, so the predicate states a scope that was
// already true rather than newly narrowing the write — which is the point: it
// is stated here, not inherited from whichever read happened to precede it.
func (r *JobRepository) Update(ctx context.Context, tenantID string, job model.Job) (model.Job, error) {
	updated := job
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewUpdate().
			Model(&updated).
			Column("status", "summary", "local_job_id", "ended_at").
			Where("job_id = ?", updated.JobID).
			Where("tenant_id = ?", tenantID).
			Returning("*").
			Scan(ctx)
		return classifyJobError(normalizeNoRows(err))
	})
	return updated, err
}
