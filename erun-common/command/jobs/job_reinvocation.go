package jobs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// considerEnvironmentJobReinvocation checks whether this turn's outcome is
// exactly the case a bounded reinvocation exists for: an agent job whose own
// process ended while the job it started had not reached a verdict, or had
// reached a bad one (see decideEnvironmentJobReinvocation for the full
// condition, including the count/budget bound). When it applies, it records the
// attempt and returns the next turn's resumed invocation; otherwise it returns
// ok=false and the caller settles the job exactly as it would without this
// feature.
//
// It calls resolveEnvironmentJobOutcome itself rather than threading its result
// back to the caller, so finishEnvironmentJob's own call on the non-reinvoking
// path recomputes the identical answer. That is not a second real wait:
// resolveEnvironmentJobOutcome only reads job records and polls a child still
// running, and by the time this function returns the children it looked at are
// already resolved one way or another — keeping one function as the single
// source of "what happened this turn" is simpler than threading the tuple
// through two call sites.
func considerEnvironmentJobReinvocation(recorder *jobRecorder, beat *jobHeartbeat, childPID int, state *os.ProcessState, waitErr error, deadline time.Time, adoptedBaseline map[int]struct{}) ([]string, bool) {
	// Folds the stream's tail before SessionID is read below, so a session id
	// the tool only reported in its very last bytes is not missed.
	beat.refresh(false)
	_, _, reason, jobState, startedJobFailed := resolveEnvironmentJobOutcome(recorder, childPID, state, waitErr, adoptedBaseline)
	job := recorder.snapshot()
	prompt, ok := decideEnvironmentJobReinvocation(job, jobState, startedJobFailed, reason, deadline)
	if !ok {
		return nil, false
	}
	resumeCommand, err := AgentJobResumeCommand(job.AgentTool, job.Progress.SessionID, prompt)
	if err != nil {
		// Should not happen: decideEnvironmentJobReinvocation already required a
		// Kind==agent job with a captured SessionID, and registerEnvironmentJob
		// already validated the tool name at start. Settle rather than loop.
		return nil, false
	}
	recorder.update(func(job *shared.EnvironmentJob) { job.ReinvocationCount++ })
	return resumeCommand, true
}

// decideEnvironmentJobReinvocation is the whole bound, in one place: every
// condition below must hold before a bounded follow-up turn runs.
//
//   - The job is an agent job. A command job's own process cannot be handed a
//     new instruction — there is no "resume" for an arbitrary argv, and no
//     reasoning to act on the outcome with.
//   - This turn's own outcome is exactly the case reinvocation exists for: the
//     job it started had not reached a verdict (gate-incomplete) or reached a
//     bad one (startedJobFailed). A plain nonzero exit with no started work
//     involved is not retried here — that would be a general auto-retry
//     feature, a materially different (and materially riskier) thing this does
//     not attempt.
//   - A session id was captured from this turn's own stream. Without one there
//     is no real conversation to resume, and reinvoking blind (a fresh,
//     context-free turn) is not what this exists to do — the honest outcome is
//     surfaced instead of a low-value blind retry.
//   - EnvironmentJobMaxReinvocations has not been reached and the wall-clock
//     EnvironmentJobReinvocationBudget has not elapsed. Both are hard caps on
//     this one job's own record (EnvironmentJob.ReinvocationCount), so a chain
//     of reinvocations is bounded by construction: it can never spawn a new
//     job, a new supervisor process, or a new lease, and it can never lengthen
//     its own bound by starting more work — whatever a reinvoked turn itself
//     starts is evaluated against this same counter and this same deadline the
//     next time this function runs.
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

// environmentJobReinvocationOutcome picks the one message that actually names
// what happened: resolveEnvironmentJobOutcome only fills reason for the
// gate-incomplete case (the started job never reached a verdict) and leaves it
// empty when startedJobFailed is what fired instead (the started job reached a
// verdict, and it was a bad one) — using reason unconditionally would hand the
// resumed turn an empty sentence in exactly that second case.
func environmentJobReinvocationOutcome(reason, startedJobFailed string) string {
	if reason != "" {
		return reason
	}
	return startedJobFailed
}

// buildEnvironmentJobReinvocationPrompt is what gives the resumed turn
// something to act on beyond "you were resumed": the concrete outcome
// (already naming the started job by id), the attempt number against the hard
// bound so the model does not treat this as an open-ended loop, and an
// explicit instruction not to end its own turn assuming another reinvocation
// will follow once the bound is reached.
//
// The task the run was originally given is restated ahead of all of it,
// recovered from the argv the job record kept (AgentJobCommandPrompt). A
// resumption that carries only the failure is answered as if it were the whole
// task: a lane whose reproduction is a probe that fails on purpose — which is
// what every reproduction is — spends its resumed turn accounting for the
// red it meant to produce, and a one-shot run's final message is the only
// channel its finding has, so the finding is gone and the job still reads as a
// clean exit. Restating the task costs nothing and is what leaves the resumed
// turn's final message answering the question it was actually asked. When the
// argv is not recoverable — a record written by a differently-shaped
// invocation, a job that ran no agent argv at all — the prompt stays exactly
// as it was, because a guess at what the task was is worse than saying
// nothing.
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
