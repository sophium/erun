package eruncommon

import "strings"

// job_report_environment.go connects an environment job's own life to the
// platform job queue; job_report.go holds the recording contract, and this is
// the caller that contract was written for. erun's own surfaces start work in
// an environment -- `erun exec job start`, and the exec_raw/exec_agent tools
// its off-environment dispatch reaches -- and until this wiring existed
// nothing populated the queue unless an actor ran `erun jobs start` by hand.
//
// The platform row is opened before the work runs and closed once the
// supervisor has observed how it ended, so a second actor reading the queue
// sees work in flight rather than only its aftermath. Both halves inherit
// ReportJobStart's best-effort contract unchanged: an environment with no
// platform alias records nothing and says nothing whatsoever, every other
// reason is a trace, and nothing here can fail -- or change the outcome of --
// the work being described. Reporting also never runs before the local record
// is durable, so a start that never got that far never opens a platform row
// that nothing would later close.

// environmentJobPlatformType is the platform job type every environment job is
// recorded as. The platform's job_type vocabulary is closed, and an
// environment job carries no declaration of which named kind it is: erun
// dispatched work into one of its own environments, and `maintenance` is the
// vocabulary's entry for exactly that -- erun's own work that is not a fix, a
// review, a gate, a release, a deploy, an investigation, a plan or a triage.
const environmentJobPlatformType = JobTypeMaintenance

// environmentJobPlatformActorKind is who the queue says holds an environment
// job: erun, dispatching work into one of its own environments. It is not a
// human -- nothing here asked a person -- and not an agent, since the AI tool
// is what the job runs rather than who holds it, so `orchestrator` is the
// honest member of the closed vocabulary.
const environmentJobPlatformActorKind = "orchestrator"

// environmentJobPlatformActorID names which orchestrator, in the shape the
// queue's actor ids already take (`<tenant>/<environment>`). A job started
// from inside another job's own work belongs to that job instead, which is
// the same fact EnvironmentJob.StartedByJobID records.
func environmentJobPlatformActorID(tenant, environment string, job EnvironmentJob) string {
	if parent := strings.TrimSpace(job.StartedByJobID); parent != "" {
		return parent
	}
	parts := make([]string, 0, 2)
	for _, part := range []string{tenant, environment} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

// environmentJobPlatformSummary is the prose the platform stores for this job.
// EnvironmentJob.Name is already the operator-facing description of what the
// work is -- the same prose the environment reports as busy -- while the argv
// stays in the pod's own record (EnvironmentJob.Command), which is where the
// platform's summary contract puts it: the API refuses a summary that is only
// a shell command, and this one is never derived from the command line.
func environmentJobPlatformSummary(job EnvironmentJob) string {
	if name := strings.TrimSpace(job.Name); name != "" {
		return name
	}
	return strings.TrimSpace(job.ID)
}

// environmentJobPlatformOutcome maps the supervisor's own verdict onto the
// platform's terminal statuses. Success is environmentJobSucceeded and nothing
// else, so a job that exited zero while leaving unsupervised work behind is
// never reported as a success it does not have. Everything without a definite
// verdict is ABANDONED rather than FAILED: a job whose outcome nothing
// observed, and a job whose captured exit status stands over work it never
// waited for, both withdrew from the queue without a result, and reporting
// either as FAILED would claim a failure that was never seen.
func environmentJobPlatformOutcome(job EnvironmentJob) string {
	switch {
	case environmentJobSucceeded(job):
		return JobStatusSucceeded
	case job.ExitCode != nil && *job.ExitCode != 0:
		return JobStatusFailed
	default:
		return JobStatusAbandoned
	}
}

// reportEnvironmentJobStart opens the platform's record of this job,
// best-effort, and returns the row's id. An empty id is the one answer a
// caller may act on -- nothing was recorded, for one of the reasons
// ReportJobStart traced -- and it closes the branch: a nil store is a caller
// with no platform wiring at all, which is not a skip worth reporting.
func reportEnvironmentJobStart(ctx Context, store CloudReadStore, deps CloudDependencies, tenant, environment string, job EnvironmentJob) string {
	if store == nil {
		return ""
	}
	return ReportJobStart(ctx, store, deps, ReportJobStartParams{
		Environment: environment,
		JobType:     environmentJobPlatformType,
		Summary:     environmentJobPlatformSummary(job),
		ActorKind:   environmentJobPlatformActorKind,
		ActorID:     environmentJobPlatformActorID(tenant, environment, job),
		LocalJobID:  job.ID,
	})
}

// reportEnvironmentJobOutcome closes the platform row this job's start opened.
// A record carrying no platform job id never opened one -- there was no alias,
// or the recording was skipped -- so there is nothing to close, and
// ReportJobOutcome says nothing about it for the same reason: the caller that
// had no id already saw why.
func reportEnvironmentJobOutcome(ctx Context, store CloudReadStore, deps CloudDependencies, job EnvironmentJob) {
	if store == nil {
		return
	}
	ReportJobOutcome(ctx, store, deps, ReportJobOutcomeParams{
		JobID:   strings.TrimSpace(job.PlatformJobID),
		Status:  environmentJobPlatformOutcome(job),
		Summary: environmentJobPlatformSummary(job),
	})
}
