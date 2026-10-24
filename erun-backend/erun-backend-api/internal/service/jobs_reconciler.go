package service

import (
	"context"
	"log"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// DefaultJobAbandonSchedule is a 6-field cron, shorter than DefaultJobAbandonTTL; the sweep is cheap and idempotent.
const DefaultJobAbandonSchedule = "0 */5 * * * *"

// JobAbandonSweeper is satisfied by *JobService, so the reconciler holds no TTL policy.
type JobAbandonSweeper interface {
	SweepAbandoned(ctx context.Context, ttl time.Duration) ([]model.Job, error)
}

// JobAbandonReconciler periodically closes RUNNING jobs whose actor stopped updating them.
type JobAbandonReconciler struct {
	jobs JobAbandonSweeper
	ttl  time.Duration
}

// NewJobAbandonReconciler wires and schedules the sweep.
func NewJobAbandonReconciler(dbosCtx dbos.DBOSContext, jobs JobAbandonSweeper, ttl time.Duration, schedule string) *JobAbandonReconciler {
	r := &JobAbandonReconciler{jobs: jobs, ttl: ttl}
	dbos.RegisterWorkflow(dbosCtx, r.tick, dbos.WithSchedule(schedule))
	return r
}

// tick runs the sweep as the operations role, since it is cross-tenant by design.
func (r *JobAbandonReconciler) tick(dctx dbos.DBOSContext, _ time.Time) (int, error) {
	// Rooted in the DBOS context so shutdown cancels the sweep.
	ctx := security.WithContext(dctx, security.Context{TenantType: string(model.TenantTypeOperations)})
	return r.reconcile(ctx)
}

func (r *JobAbandonReconciler) reconcile(ctx context.Context) (int, error) {
	abandoned, err := r.jobs.SweepAbandoned(ctx, r.ttl)
	if err != nil {
		return 0, err
	}
	for _, job := range abandoned {
		log.Printf("jobs: abandoned %s (actor %s, scope %q, started %s): %s",
			job.JobID, job.ActorID, job.Scope, job.StartedAt.UTC().Format(time.RFC3339), job.Summary)
	}
	return len(abandoned), nil
}
