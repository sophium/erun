package jobs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// agentAuthFailureSignatures mark an AI tool's own auth failure, which means the
// environment's credentials are missing or stale.
var agentAuthFailureSignatures = []string{
	"oauth session expired",
	"failed to authenticate",
	"authentication_error",
	"invalid api key",
	"please run /login",
	"not logged in",
}

// agentGatewayRoundTripFailureSignatures mark a gateway refusing a conversation
// whose reasoning it never returned; no client can replay it.
var agentGatewayRoundTripFailureSignatures = []string{
	"`reasoning_content` in the thinking mode",
}

// agentFailureReason clarifies a known failure phrasing, or returns "".
func agentFailureReason(tool, message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, signature := range agentAuthFailureSignatures {
		if strings.Contains(lower, signature) {
			return fmt.Sprintf("%s reported an authentication failure (%s); this environment's %s credentials are missing or stale, not a problem with the work itself", tool, trimmed, tool)
		}
	}
	for _, signature := range agentGatewayRoundTripFailureSignatures {
		if strings.Contains(lower, signature) {
			return fmt.Sprintf("%s was refused mid-run by the gateway model's provider (%s): the model's reasoning cannot be echoed back through this gateway, so the conversation was rejected mid-run rather than the work failing; the working tree it left is preserved, and dispatching the work again starts a fresh conversation", tool, trimmed)
		}
	}
	return ""
}

// finishEnvironmentJob records the outcome from waiting on the process, never
// from parsing output. Reclaim is decided on an in-memory settled copy so the
// single durable write never shows "finished" while the clone still exists.
func finishEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, writer *jobOutputWriter, childPID int, state *os.ProcessState, waitErr error, adoptedBaseline map[int]struct{}) error {
	// Fold the stream's tail first so the finished record carries the last activity.
	beat.refresh(false)

	cancelledBy, wasCancelled := consumeEnvironmentJobCancelRequest(recorder.dir, recorder.snapshot().ID)

	code, signal, reason, jobState, startedJobFailed := resolveEnvironmentJobOutcome(recorder, childPID, state, waitErr, adoptedBaseline)
	// Reinvocations are spent; say so instead of a first-run reason.
	if count := recorder.snapshot().ReinvocationCount; count > 0 && (jobState == shared.EnvironmentJobStateGateIncomplete || startedJobFailed != "") {
		note := fmt.Sprintf("already resumed %d time(s) without a clean outcome; the reinvocation bound is exhausted", count)
		if reason == "" {
			reason = note
		} else {
			reason = reason + " (" + note + ")"
		}
	}
	worktree := captureAgentJobWorktreeOutcome(recorder.snapshot(), recorder.dir)
	// Taken before reclaim so EndedAt does not include reclaim's own time.
	endedAt := time.Now()
	settle := func(job *shared.EnvironmentJob) {
		job.State = jobState
		job.EndedAt = endedAt
		job.OutputBytes = writer.written()
		job.OutputTruncated = writer.truncated()
		job.Signal = signal
		if reason == "" && code != 0 && job.Kind == EnvironmentJobKindAgent && job.Progress != nil {
			reason = agentFailureReason(job.AgentTool, job.Progress.Error)
		}
		job.Reason = reason
		job.ExitCode = &code
		job.StartedJobFailed = startedJobFailed
		if wasCancelled {
			job.CancelledByJobID = cancelledBy
		}
		worktree.apply(job)
	}

	settled := recorder.snapshot()
	settle(&settled)
	reclaimed, cloneReason := reclaimAgentJobWorkClone(settled)

	recorder.update(func(job *shared.EnvironmentJob) {
		settle(job)
		job.CloneReclaimed = reclaimed
		job.CloneKeptReason = cloneReason
	})
	return nil
}

// resolveEnvironmentJobOutcome decides code, signal, reason, and state.
// Leftover processes (group, session, or adopted descendants) mark it abandoned
// at once; running started jobs are awaited, and only a wait timeout marks it
// gate-incomplete.
func resolveEnvironmentJobOutcome(recorder *jobRecorder, childPID int, state *os.ProcessState, waitErr error, adoptedBaseline map[int]struct{}) (code int, signal, reason, jobState, startedJobFailed string) {
	code = -1
	switch {
	case state != nil:
		code = state.ExitCode()
		if signal = core.EnvironmentJobExitSignal(state); signal != "" {
			reason = "terminated by signal " + signal
		}
	case waitErr != nil:
		reason = "failed to start: " + waitErr.Error()
	}
	self := recorder.snapshot()
	jobState = shared.EnvironmentJobStateExited
	if state != nil && (core.EnvironmentJobProcessGroupSurvivors(childPID) ||
		core.EnvironmentJobSessionSurvivors(childPID) ||
		core.EnvironmentJobDescendantSurvivors(os.Getpid(), environmentJobLeftoverExclusions(recorder.dir, self.ID, adoptedBaseline))) {
		jobState = shared.EnvironmentJobStateAbandoned
		reason = "the job's own process exited, but it left other processes still running in its process group, session, or reparented onto this supervisor — background work it started and never waited for; nothing further will be reported for that work"
	}
	if running := awaitEnvironmentJobRunningChildren(recorder.dir, self.ID, resolveEnvironmentJobGateIncompleteWaitCap()); len(running) > 0 {
		jobState = shared.EnvironmentJobStateGateIncomplete
		reason = environmentJobGateIncompleteReason(running)
		return code, signal, reason, jobState, startedJobFailed
	}
	if failed := environmentJobFailedChildren(recorder.dir, self.ID, time.Now()); len(failed) > 0 {
		startedJobFailed = environmentJobFailedChildReason(failed)
	}
	return code, signal, reason, jobState, startedJobFailed
}

// environmentJobLeftoverExclusions are pids the finish check must not count as
// abandoned work: the baseline plus supervisors of jobs this one handed off,
// which reparent onto this process by design.
func environmentJobLeftoverExclusions(dir, parentID string, adoptedBaseline map[int]struct{}) map[int]struct{} {
	excluded := adoptedBaseline
	for _, child := range environmentJobChildren(dir, parentID, time.Now()) {
		if !child.Handoff || child.PID <= 0 {
			continue
		}
		if _, known := excluded[child.PID]; known {
			continue
		}
		merged := make(map[int]struct{}, len(excluded)+1)
		for pid := range excluded {
			merged[pid] = struct{}{}
		}
		merged[child.PID] = struct{}{}
		excluded = merged
	}
	return excluded
}

// awaitEnvironmentJobRunningChildren polls until no non-handoff child runs or
// waitCap elapses, returning those still running.
func awaitEnvironmentJobRunningChildren(dir, parentID string, waitCap time.Duration) []shared.EnvironmentJob {
	poll := resolveEnvironmentJobGateIncompletePoll()
	deadline := time.Now().Add(waitCap)
	for {
		running := environmentJobRunningChildren(dir, parentID, time.Now())
		if len(running) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return running
		}
		remaining := time.Until(deadline)
		if remaining < poll {
			poll = remaining
		}
		time.Sleep(poll)
	}
}

func environmentJobGateIncompleteReason(running []shared.EnvironmentJob) string {
	ids := make([]string, 0, len(running))
	for _, job := range running {
		ids = append(ids, job.ID)
	}
	noun := "job"
	if len(ids) != 1 {
		noun = "jobs"
	}
	return fmt.Sprintf("this job ended while the %s it started (%s) had not reached a verdict; await it directly for the real outcome instead of treating this job's own exit as the answer", noun, strings.Join(ids, ", "))
}

func environmentJobFailedChildReason(failed []shared.EnvironmentJob) string {
	ids := make([]string, 0, len(failed))
	for _, job := range failed {
		ids = append(ids, job.ID)
	}
	noun := "job"
	if len(ids) != 1 {
		noun = "jobs"
	}
	return fmt.Sprintf("the %s this job started (%s) did not succeed", noun, strings.Join(ids, ", "))
}
