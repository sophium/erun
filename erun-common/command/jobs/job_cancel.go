package jobs

import (
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

type CancelEnvironmentJobResult struct {
	Job jobstate.EnvironmentJob `json:"job"`
	// Signalled is false when the job had already finished.
	Signalled bool   `json:"signalled"`
	Signal    string `json:"signal,omitempty"`
	TargetPID int    `json:"targetPid,omitempty"`
}

// resolveEnvironmentJobCancelTarget is the one place that ensures a job's own
// supervisor is never signalled.
func resolveEnvironmentJobCancelTarget(job jobstate.EnvironmentJob) (int, error) {
	// A task job's PID is this process; there is no subprocess to signal.
	if job.Kind == EnvironmentJobKindTask {
		return 0, fmt.Errorf("job %q is a background task job with no subprocess to signal; wait for it or let it finish", job.ID)
	}
	if job.ChildPID > 0 {
		return job.ChildPID, nil
	}
	// For a started job PID is the supervisor, which must survive to record the
	// outcome; only an attached job's PID is the work.
	if job.Attached && job.PID > 0 {
		return job.PID, nil
	}
	return 0, fmt.Errorf("job %q has not recorded its work's process yet, so there is nothing to signal here but the supervisor; retry in a moment or wait for the job to start", job.ID)
}

// CancelEnvironmentJob signals the recorded pid, never a command-line pattern
// that could match the caller's shell, and never the supervisor.
func CancelEnvironmentJob(ctx core.Context, params model.CancelEnvironmentJobParams) (CancelEnvironmentJobResult, error) {
	signal, err := core.NormalizeEnvironmentJobSignal(params.Signal)
	if err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
	if err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result := CancelEnvironmentJobResult{Job: job, Signal: signal}
	if job.Finished() {
		ctx.Trace(fmt.Sprintf("job: %s already finished (%s), nothing to signal", job.ID, job.State))
		return result, nil
	}
	target, err := resolveEnvironmentJobCancelTarget(job)
	if err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result.TargetPID = target
	ctx.Trace(fmt.Sprintf("job: sending SIG%s to process group %d (job %s)", signal, target, job.ID))
	if ctx.DryRun {
		return result, nil
	}
	// Recorded before the signal: the target's finish check runs concurrently.
	// Best-effort; failure costs only the provenance.
	if dir, dirErr := jobexclusive.EnvironmentJobDir(params.Tenant, params.Environment); dirErr == nil {
		recordEnvironmentJobCancelRequest(dir, job.ID, jobstate.CurrentEnvironmentJobID())
	}
	if err := core.SignalEnvironmentJobProcessGroup(target, signal); err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result.Signalled = true
	return result, nil
}
