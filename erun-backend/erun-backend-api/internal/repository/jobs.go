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

// JobFilter composes GET /v1/jobs filters, AND-ed together.
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

// classifyJobError maps constraint violations to sentinel errors; an invisible
// environmentId fails the FK like a missing one.
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

// Get returns tenantID's job, or ErrNotFound. tenantID is an explicit predicate
// because erun_operations' RLS policy is unconditional.
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

// List returns the tenant's jobs, open (PLANNED/RUNNING) first, then most
// recently started. tenant_id is filtered explicitly because erun_operations'
// RLS policy is unconditional.
func (r *JobRepository) List(ctx context.Context, filter JobFilter) ([]model.Job, error) {
	return r.list(ctx, filter, "")
}

// ListByEnvironment is List narrowed to one environment.
func (r *JobRepository) ListByEnvironment(ctx context.Context, environmentID string) ([]model.Job, error) {
	return r.list(ctx, JobFilter{}, environmentID)
}

func (r *JobRepository) list(ctx context.Context, filter JobFilter, environmentID string) ([]model.Job, error) {
	// Non-nil so it marshals as [] rather than null.
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
		query += ` ORDER BY (status IN ('PLANNED', 'RUNNING')) DESC, started_at DESC, job_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &jobs)
	})
	return jobs, err
}

// FindOpenByScope returns the open job holding scope, or ErrNotFound. PLANNED
// counts as holding.
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
			   AND status IN ('PLANNED', 'RUNNING')
			 ORDER BY started_at DESC, job_id DESC
			 LIMIT 1
		`, securityContext.TenantID, scope).Scan(ctx, &job)
		return normalizeNoRows(err)
	})
	return job, err
}

// AbandonStale closes, across all tenants, every RUNNING job not updated since
// staleBefore, in one statement so a concurrent refresh is not lost. PLANNED
// jobs are never swept: they have no actor to go stale.
func (r *JobRepository) AbandonStale(ctx context.Context, staleBefore time.Time) ([]model.Job, error) {
	// Non-nil so it marshals as [] rather than null.
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

// Update persists status, summary, local_job_id and ended_at; all other fields
// are immutable so a job cannot be reassigned. tenantID is an explicit predicate.
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
