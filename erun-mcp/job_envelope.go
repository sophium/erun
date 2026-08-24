package erunmcp

import (
	eruncommon "github.com/sophium/erun/erun-common"
)

// The tools converted here call shared erun-common logic directly in this
// process rather than shelling out, so job-ifying them means running that
// same call asynchronously in a background task job (eruncommon.
// StartTaskEnvironmentJob) instead of detaching a subprocess the way job_start
// always did. wait is the one-release compatibility switch: unset or true
// keeps today's behaviour, a full synchronous CommandOutput; false starts the
// work as a background job and returns its handle immediately, with the
// caller reading the typed result back from exec_job_status/exec_job_output
// once it finishes.
//
// jobEnvelopeDefaultWaitsSynchronously is this release's default. The plan is
// to flip it to false (async becomes the default) in the release after this
// one, keeping wait:true available for one more release as the explicit
// compatibility switch before it is removed — the same one-release window
// used for the exec_* rename aliases (#1186).
const jobEnvelopeDefaultWaitsSynchronously = true

// JobEnvelopeOutput is what every job-ified work-performing tool returns.
type JobEnvelopeOutput struct {
	CommandOutput
	// JobID addresses the background job when Wait is false; empty when Wait
	// is true, since the call already carries the full result.
	JobID string `json:"jobId,omitempty"`
	// State is the job's state at the moment this call returned — always
	// "running" for an async start, since the call returns as soon as the
	// job record exists and before the work has had a chance to finish.
	State string `json:"state,omitempty"`
	// Wait says which of the two shapes this response actually is, so a
	// caller need not infer it from which fields happen to be set.
	Wait bool `json:"wait"`
}

func waitRequested(wait *bool) bool {
	if wait == nil {
		return jobEnvelopeDefaultWaitsSynchronously
	}
	return *wait
}

// runJobEnvelope resolves the wait/preview decision and either runs execute
// inline or hands it to a background task job. execute must build its own
// CommandOutput, including attaching any tool-specific typed extra (Pin,
// Write, Spec, ...) before returning it — that keeps every value execute
// touches local to its own call, whether that call happens now on this
// goroutine or later on the job's, so there is nothing for the two to race on.
func runJobEnvelope(runtime RuntimeConfig, jobName string, wait *bool, preview bool, execute func(preview bool) (CommandOutput, error)) (JobEnvelopeOutput, error) {
	if preview || waitRequested(wait) {
		output, err := execute(preview)
		return JobEnvelopeOutput{CommandOutput: output, Wait: true}, err
	}

	job, err := eruncommon.StartTaskEnvironmentJob(eruncommon.TaskEnvironmentJobParams{
		Tenant:      runtime.Context.Tenant,
		Environment: runtime.Context.Environment,
		Name:        jobName,
		ID:          jobName,
		Run: func() (any, error) {
			return execute(false)
		},
	})
	if err != nil {
		return JobEnvelopeOutput{}, err
	}
	return JobEnvelopeOutput{JobID: job.ID, State: job.State, Wait: false}, nil
}

// simpleJobExecute adapts a plain runRuntimeCommand call (no tool-specific
// extra field to attach) into the execute shape runJobEnvelope wants.
func simpleJobExecute(runtime RuntimeConfig, verbosity int, run func(eruncommon.Context, string) error) func(bool) (CommandOutput, error) {
	return func(preview bool) (CommandOutput, error) {
		return runRuntimeCommand(runtime, preview, verbosity, run)
	}
}
