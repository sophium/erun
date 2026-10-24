package shared

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/jobs"
)

// jobEnvelopeDefaultWaitsSynchronously: unset wait runs the call synchronously;
// wait:false runs it as a background task job and returns its handle.
const jobEnvelopeDefaultWaitsSynchronously = true

// JobEnvelopeInput is the async-start input embedded in every job-ified tool.
type JobEnvelopeInput struct {
	Wait           *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
	Handoff        bool   `json:"handoff,omitempty" jsonschema:"mark the background job as deliberately meant to outlive whatever starts it; only used with wait false. When this call itself runs from inside another job's own work, that job otherwise waits for this one to reach a verdict before reporting its own outcome; set true for work meant to keep running past the caller's own turn on purpose (a release, a long deploy)"`
	StartedByJobID string `json:"startedByJobId,omitempty" jsonschema:"internal: the job this call is being made on behalf of, so that job's own finish check can find this one as work it started; only used with wait false. This tool runs its work inside this server's own long-lived process, which was never itself started as anyone's job and so has no ERUN_JOB_ID to inherit however deep the nesting is on the calling side -- a caller that is itself running as a job and wants this work covered by its own finish check has to pass its job id here"`
}

// JobEnvelopeOutput is what every job-ified tool returns.
type JobEnvelopeOutput struct {
	CommandOutput
	// JobID is empty when Wait is true.
	JobID string `json:"jobId,omitempty"`
	// State is always "running" for an async start.
	State string `json:"state,omitempty"`
	Wait  bool   `json:"wait"`
}

func WaitRequested(wait *bool) bool {
	if wait == nil {
		return jobEnvelopeDefaultWaitsSynchronously
	}
	return *wait
}

// RunJobEnvelope runs execute inline or as a background task job. execute must
// build its whole CommandOutput itself so nothing it touches is shared across goroutines.
func RunJobEnvelope(runtime RuntimeConfig, jobName string, envelope JobEnvelopeInput, preview bool, execute func(preview bool, log io.Writer) (CommandOutput, error)) (JobEnvelopeOutput, error) {
	if preview || WaitRequested(envelope.Wait) {
		output, err := execute(preview, io.Discard)
		return JobEnvelopeOutput{CommandOutput: output, Wait: true}, err
	}

	job, err := jobs.StartTaskEnvironmentJob(jobs.TaskEnvironmentJobParams{
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

// SimpleJobExecute adapts a plain RunRuntimeCommand call to RunJobEnvelope's execute shape.
func SimpleJobExecute(runtime RuntimeConfig, verbosity int, run func(core.Context, string) error) func(bool, io.Writer) (CommandOutput, error) {
	return func(preview bool, log io.Writer) (CommandOutput, error) {
		return RunRuntimeCommand(runtime, preview, verbosity, log, run)
	}
}
