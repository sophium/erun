package jobs

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

func AttachEnvironmentJob(ctx core.Context, params model.AttachEnvironmentJobParams) (jobstate.EnvironmentJob, error) {
	dir, job, ttl, err := resolveAttachedEnvironmentJob(params)
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	ctx.Trace(fmt.Sprintf("job: attaching %q as job %s, tracking pid %d", job.Name, job.ID, job.PID))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s while pid %d lives", job.LeaseID, job.PID))
	if ctx.DryRun {
		return job, nil
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if _, err := activitylease.TakeEnvironmentActivityLease(activitylease.TakeEnvironmentActivityLeaseParams{
		Tenant:      params.Tenant,
		Environment: params.Environment,
		Name:        job.Name,
		ID:          job.LeaseID,
		PID:         job.PID,
		TTL:         ttl,
		Holder:      environmentJobHolder(params.Tenant),
	}); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	return job, nil
}

func resolveAttachedEnvironmentJob(params model.AttachEnvironmentJobParams) (string, jobstate.EnvironmentJob, time.Duration, error) {
	name := strings.TrimSpace(params.Name)
	id, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, name, params.ID)
	if err != nil {
		return "", jobstate.EnvironmentJob{}, 0, err
	}
	if params.PID <= 0 {
		return "", jobstate.EnvironmentJob{}, 0, fmt.Errorf("a pid to track is required; an attached job has nothing else to reconcile against")
	}
	ttl, err := normalizeEnvironmentJobLeaseTTL(params.LeaseTTL)
	if err != nil {
		return "", jobstate.EnvironmentJob{}, 0, err
	}
	dir, err := jobexclusive.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return "", jobstate.EnvironmentJob{}, 0, err
	}
	job := jobstate.EnvironmentJob{
		ID:        id,
		Name:      name,
		State:     EnvironmentJobStateRunning,
		PID:       params.PID,
		StartedAt: time.Now(),
		Attached:  true,
		LogPath:   strings.TrimSpace(params.LogPath),
		LeaseID:   environmentJobLeaseID(id),
	}
	// Re-attaching renews rather than restarts, so callers can refresh on a timer.
	if existing, err := jobstate.ReadEnvironmentJob(filepath.Join(dir, id+".json")); err == nil && existing.Attached && existing.PID == params.PID {
		job.StartedAt = existing.StartedAt
	}
	job.OutputBytes = environmentJobOutputSize(job.LogPath, 0)
	return dir, job, ttl, nil
}
