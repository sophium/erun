package service

import (
	"context"
	"log"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// DefaultJobAbandonSchedule is how often the sweep runs, as a standard
// (6-field, second-precision) cron expression — the same form
// provision.DefaultDeleteReconcileSchedule uses. It is deliberately shorter
// than DefaultJobAbandonTTL: the sweep is cheap and idempotent, and running
// it more often than the TTL only bounds how long an abandoned job keeps
// holding its scope past the TTL, never how eagerly a live job is closed.
const DefaultJobAbandonSchedule = "0 */5 * * * *"

// JobAbandonSweeper is the sweep the reconciler runs. It is satisfied by
// *JobService, so the reconciler holds no TTL policy of its own.
type JobAbandonSweeper interface {
	SweepAbandoned(ctx context.Context, ttl time.Duration) ([]model.Job, error)
}

// JobAbandonReconciler closes RUNNING jobs whose actor stopped updating them,
// so a scope held by a process that is gone is released without an operator
// noticing and asking for it. It runs as a DBOS scheduled workflow, the
// platform-wide cron primitive the environment-delete reconciler already
// uses for the same shape of work: a periodic repair that no request
// triggers.
type JobAbandonReconciler struct {
	jobs JobAbandonSweeper
	ttl  time.Duration
}

// NewJobAbandonReconciler wires and schedules the sweep. schedule is a
// standard (6-field, second-precision) cron expression.
func NewJobAbandonReconciler(dbosCtx dbos.DBOSContext, jobs JobAbandonSweeper, ttl time.Duration, schedule string) *JobAbandonReconciler {
	r := &JobAbandonReconciler{jobs: jobs, ttl: ttl}
	dbos.RegisterWorkflow(dbosCtx, r.tick, dbos.WithSchedule(schedule))
	return r
}

// tick is the scheduled-workflow entrypoint DBOS calls on each cron fire; it
// exists only to supply the operations-scoped context the sweep needs, kept
// separate so the sweep itself is a plain function a test can call directly
// against fakes without a live DBOS scheduler.
//
// The sweep is cross-tenant by design, so it runs as the operations role:
// one abandoned job in one tenant must not need that tenant to ask for it to
// be cleared, and the surrounding transaction picks erun_operations from this
// context's tenant type.
func (r *JobAbandonReconciler) tick(dctx dbos.DBOSContext, _ time.Time) (int, error) {
	// Rooted in the DBOS context, not context.Background(): a sweep on a
	// background context is uncancellable and keeps running straight through
	// a shutdown, the same reason the environment-delete reconciler's tick
	// derives its context this way.
	ctx := security.WithContext(dctx, security.Context{TenantType: string(model.TenantTypeOperations)})
	return r.reconcile(ctx)
}

// reconcile closes every job past its TTL and reports how many it closed, for
// the caller to log. A sweep that closed nothing is not an error: on a quiet
// platform it is the ordinary result.
func (r *JobAbandonReconciler) reconcile(ctx context.Context) (int, error) {
	abandoned, err := r.jobs.SweepAbandoned(ctx, r.ttl)
	if err != nil {
		return 0, err
	}
	for _, job := range abandoned {
		// Named individually rather than only counted: an abandoned job means
		// an actor vanished mid-work, and which scope it held is the thing a
		// reader needs to judge whether anything was left half-done.
		log.Printf("jobs: abandoned %s (actor %s, scope %q, started %s): %s",
			job.JobID, job.ActorID, job.Scope, job.StartedAt.UTC().Format(time.RFC3339), job.Summary)
	}
	return len(abandoned), nil
}
