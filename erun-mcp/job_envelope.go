package erunmcp

import (
	"io"

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

// JobEnvelopeInput is the async-start half of every job-ified tool's input,
// embedded so all of them describe the same three switches once rather than
// seventeen times. Embedding flattens on the wire and in the published JSON
// schema, so each tool's input shape is unchanged by carrying it.
type JobEnvelopeInput struct {
	Wait           *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
	Handoff        bool   `json:"handoff,omitempty" jsonschema:"mark the background job as deliberately meant to outlive whatever starts it; only used with wait false. When this call itself runs from inside another job's own work, that job otherwise waits for this one to reach a verdict before reporting its own outcome; set true for work meant to keep running past the caller's own turn on purpose (a release, a long deploy)"`
	StartedByJobID string `json:"startedByJobId,omitempty" jsonschema:"internal: the job this call is being made on behalf of, so that job's own finish check can find this one as work it started; only used with wait false. This tool runs its work inside this server's own long-lived process, which was never itself started as anyone's job and so has no ERUN_JOB_ID to inherit however deep the nesting is on the calling side -- a caller that is itself running as a job and wants this work covered by its own finish check has to pass its job id here"`
}

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
//
// The log writer execute receives is the background job's own log, mirrored
// as the work produces it so a poll of exec_job_output shows progress rather
// than only a verdict, and so a failure leaves something behind to read. A
// synchronous call has no job and no log, and returns everything inline
// anyway, so it gets io.Discard.
func runJobEnvelope(runtime RuntimeConfig, jobName string, envelope JobEnvelopeInput, preview bool, execute func(preview bool, log io.Writer) (CommandOutput, error)) (JobEnvelopeOutput, error) {
	if preview || waitRequested(envelope.Wait) {
		output, err := execute(preview, io.Discard)
		return JobEnvelopeOutput{CommandOutput: output, Wait: true}, err
	}

	job, err := eruncommon.StartTaskEnvironmentJob(eruncommon.TaskEnvironmentJobParams{
		Tenant:         runtime.Context.Tenant,
		Environment:    runtime.Context.Environment,
		Name:           jobName,
		ID:             jobName,
		StartedByJobID: envelope.StartedByJobID,
		Handoff:        envelope.Handoff,
		Run: func(log io.Writer) (any, error) {
			return execute(false, log)
		},
	})
	if err != nil {
		return JobEnvelopeOutput{}, err
	}
	return JobEnvelopeOutput{JobID: job.ID, State: job.State, Wait: false}, nil
}

// simpleJobExecute adapts a plain runRuntimeCommand call (no tool-specific
// extra field to attach) into the execute shape runJobEnvelope wants.
func simpleJobExecute(runtime RuntimeConfig, verbosity int, run func(eruncommon.Context, string) error) func(bool, io.Writer) (CommandOutput, error) {
	return func(preview bool, log io.Writer) (CommandOutput, error) {
		return runRuntimeCommand(runtime, preview, verbosity, log, run)
	}
}
