package jobs

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// AttachEnvironmentJobParams registers work erun did not start.
type AttachEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Name        string
	// PID is the process to track. It is the only thing the attached job's state
	// is decided by, and erun never learns its exit status — nothing erun ran was
	// waiting on it — so it resolves to unknown rather than to a made-up outcome.
	PID int
	// LogPath is where the caller is already writing the work's output, so job
	// output can serve it without erun having captured it.
	LogPath  string
	LeaseTTL time.Duration
}

// AttachEnvironmentJob gives work started another way a handle and a lease.
func AttachEnvironmentJob(ctx core.Context, params AttachEnvironmentJobParams) (shared.EnvironmentJob, error) {
	dir, job, ttl, err := resolveAttachedEnvironmentJob(params)
	if err != nil {
		return shared.EnvironmentJob{}, err
	}
	ctx.Trace(fmt.Sprintf("job: attaching %q as job %s, tracking pid %d", job.Name, job.ID, job.PID))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s while pid %d lives", job.LeaseID, job.PID))
	if ctx.DryRun {
		return job, nil
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return shared.EnvironmentJob{}, err
	}
	if _, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      params.Tenant,
		Environment: params.Environment,
		Name:        job.Name,
		ID:          job.LeaseID,
		PID:         job.PID,
		TTL:         ttl,
		Holder:      environmentJobHolder(params.Tenant),
	}); err != nil {
		return shared.EnvironmentJob{}, err
	}
	return job, nil
}

func resolveAttachedEnvironmentJob(params AttachEnvironmentJobParams) (string, shared.EnvironmentJob, time.Duration, error) {
	name := strings.TrimSpace(params.Name)
	id, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, name, params.ID)
	if err != nil {
		return "", shared.EnvironmentJob{}, 0, err
	}
	if params.PID <= 0 {
		return "", shared.EnvironmentJob{}, 0, fmt.Errorf("a pid to track is required; an attached job has nothing else to reconcile against")
	}
	ttl, err := normalizeEnvironmentJobLeaseTTL(params.LeaseTTL)
	if err != nil {
		return "", shared.EnvironmentJob{}, 0, err
	}
	dir, err := shared.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return "", shared.EnvironmentJob{}, 0, err
	}
	job := shared.EnvironmentJob{
		ID:        id,
		Name:      name,
		State:     EnvironmentJobStateRunning,
		PID:       params.PID,
		StartedAt: time.Now(),
		Attached:  true,
		LogPath:   strings.TrimSpace(params.LogPath),
		LeaseID:   environmentJobLeaseID(id),
	}
	// Re-attaching the same id renews rather than restarts, matching the lease
	// store, so a caller can refresh on a timer without tracking what it holds.
	if existing, err := shared.ReadEnvironmentJob(filepath.Join(dir, id+".json")); err == nil && existing.Attached && existing.PID == params.PID {
		job.StartedAt = existing.StartedAt
	}
	job.OutputBytes = environmentJobOutputSize(job.LogPath, 0)
	return dir, job, ttl, nil
}
