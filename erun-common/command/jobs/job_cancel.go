package jobs

import (
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// CancelEnvironmentJobParams selects the job to signal.
type CancelEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	// Signal is TERM, INT, HUP, or KILL; empty means TERM.
	Signal string
}

// CancelEnvironmentJobResult reports what the cancel did.
type CancelEnvironmentJobResult struct {
	Job shared.EnvironmentJob `json:"job"`
	// Signalled is false when the job had already finished, so a cancel that
	// raced the work's own exit is not reported as having stopped it.
	Signalled bool   `json:"signalled"`
	Signal    string `json:"signal,omitempty"`
	TargetPID int    `json:"targetPid,omitempty"`
}

// resolveEnvironmentJobCancelTarget names the process a cancel is to signal, or
// explains why it cannot name one. It is the only place that decides this, so
// the rule that a job's own supervisor is never a target lives in one place.
func resolveEnvironmentJobCancelTarget(job shared.EnvironmentJob) (int, error) {
	// A task job's PID is this process's own -- there is no subprocess to fall
	// back to signalling, and falling back anyway would send the signal to
	// whatever is running this call instead of refusing outright.
	if job.Kind == EnvironmentJobKindTask {
		return 0, fmt.Errorf("job %q is a background task job with no subprocess to signal; wait for it or let it finish", job.ID)
	}
	if job.ChildPID > 0 {
		return job.ChildPID, nil
	}
	// Falling back to PID is only ever right for an attached job, where PID
	// *is* the process the caller attached. For a started job PID is the
	// supervisor, and that is the one process a cancel must never reach: it is
	// what has to survive to record the outcome, and killing it leaves the
	// work running with nothing left to report for it. A started job's record
	// with no ChildPID is one a reader observed before the supervisor named
	// the work; see awaitEnvironmentJobRecord, which stops that state being
	// handed back as a start handle at all.
	if job.Attached && job.PID > 0 {
		return job.PID, nil
	}
	return 0, fmt.Errorf("job %q has not recorded its work's process yet, so there is nothing to signal here but the supervisor; retry in a moment or wait for the job to start", job.ID)
}

// CancelEnvironmentJob signals the work behind a job. The target is the pid the
// record holds, never a command-line pattern — a pattern can match the caller's
// own shell, which is how a cancel once killed the sequence issuing it. The
// supervisor is deliberately not signalled, so it survives to record the
// outcome the cancel produced.
func CancelEnvironmentJob(ctx core.Context, params CancelEnvironmentJobParams) (CancelEnvironmentJobResult, error) {
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
	// Recorded before the signal reaches the target, so its own finish check
	// -- which runs concurrently with this call, not after it -- has the
	// marker in place by the time it looks for one. Best-effort like the
	// signal itself: a write that fails here costs only the provenance, not
	// the cancel.
	if dir, dirErr := shared.EnvironmentJobDir(params.Tenant, params.Environment); dirErr == nil {
		recordEnvironmentJobCancelRequest(dir, job.ID, shared.CurrentEnvironmentJobID())
	}
	if err := core.SignalEnvironmentJobProcessGroup(target, signal); err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result.Signalled = true
	return result, nil
}
