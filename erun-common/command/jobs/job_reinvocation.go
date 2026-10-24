package jobs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// considerEnvironmentJobReinvocation returns the resumed invocation when this
// turn qualifies for a bounded reinvocation, recording the attempt.
func considerEnvironmentJobReinvocation(recorder *jobRecorder, beat *jobHeartbeat, childPID int, state *os.ProcessState, waitErr error, deadline time.Time, adoptedBaseline map[int]struct{}) ([]string, bool) {
	// Fold the tail first so a session id reported in the last bytes is seen.
	beat.refresh(false)
	_, _, reason, jobState, startedJobFailed := resolveEnvironmentJobOutcome(recorder, childPID, state, waitErr, adoptedBaseline)
	job := recorder.snapshot()
	prompt, ok := decideEnvironmentJobReinvocation(job, jobState, startedJobFailed, reason, deadline)
	if !ok {
		return nil, false
	}
	resumeCommand, err := AgentJobResumeCommand(job.AgentTool, job.Progress.SessionID, prompt)
	if err != nil {
		return nil, false
	}
	recorder.update(func(job *shared.EnvironmentJob) { job.ReinvocationCount++ })
	return resumeCommand, true
}

// decideEnvironmentJobReinvocation holds the whole bound: an agent job, a
// started job that is unfinished or failed (not a plain nonzero exit), a
// captured session id, and both the count and wall-clock caps unspent.
func decideEnvironmentJobReinvocation(job shared.EnvironmentJob, jobState, startedJobFailed, reason string, deadline time.Time) (string, bool) {
	if job.Kind != EnvironmentJobKindAgent {
		return "", false
	}
	if jobState != shared.EnvironmentJobStateGateIncomplete && startedJobFailed == "" {
		return "", false
	}
	if job.Progress == nil || strings.TrimSpace(job.Progress.SessionID) == "" {
		return "", false
	}
	if job.ReinvocationCount >= resolveEnvironmentJobMaxReinvocations() {
		return "", false
	}
	if !time.Now().Before(deadline) {
		return "", false
	}
	return buildEnvironmentJobReinvocationPrompt(job, environmentJobReinvocationOutcome(reason, startedJobFailed)), true
}

// environmentJobReinvocationOutcome prefers reason, which is empty when
// startedJobFailed fired.
func environmentJobReinvocationOutcome(reason, startedJobFailed string) string {
	if reason != "" {
		return reason
	}
	return startedJobFailed
}

// buildEnvironmentJobReinvocationPrompt restates the original task when it is
// recoverable, so the resumed turn's final message still answers it.
func buildEnvironmentJobReinvocationPrompt(job shared.EnvironmentJob, outcome string) string {
	maxReinvocations := resolveEnvironmentJobMaxReinvocations()
	attempt := job.ReinvocationCount + 1
	task := AgentJobCommandPrompt(job.AgentTool, job.Command)
	if task == "" {
		return fmt.Sprintf(
			"%s This is an automatic, bounded resumption of your own session (attempt %d of %d) so you can act on the real outcome: check the actual current state of what you started, fix or verify it directly, and either resolve this conclusively or explain clearly why it cannot be resolved right now. There is no further resumption once this bound is reached, so do not end this turn assuming another one will follow.",
			outcome, attempt, maxReinvocations)
	}
	return fmt.Sprintf(
		"The task you were given was: %s\n\n%s This is an automatic, bounded resumption of your own session (attempt %d of %d) on top of that task, not a replacement for it, so you can act on the real outcome: check the actual current state of what you started, fix or verify it directly, and either resolve this conclusively or explain clearly why it cannot be resolved right now. Your final message is the only place your answer to the task above is read from, so it must still deliver that answer, not only what you conclude about this resumption. There is no further resumption once this bound is reached, so do not end this turn assuming another one will follow.",
		task, outcome, attempt, maxReinvocations)
}
