package jobs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// agentAuthFailureSignatures are phrases the AI tools use for their own auth
// failures. Matching one against a failed agent job's folded progress is what
// turns "the tool's raw message" into "this environment's credentials for
// that tool are missing or stale" — the actual problem, since there is often
// no in-band way to refresh them, unlike what the raw message suggests.
var agentAuthFailureSignatures = []string{
	"oauth session expired",
	"failed to authenticate",
	"authentication_error",
	"invalid api key",
	"please run /login",
	"not logged in",
}

// agentGatewayRoundTripFailureSignatures are phrases a gateway serving a
// reasoning model uses when it refuses a conversation because it must be handed
// the model's own reasoning back. The refusal is the provider's, and it lands
// mid-run however long the conversation has already run: the client can only
// echo reasoning the gateway returned, so a turn whose reasoning the gateway
// withheld cannot be replayed by any client, including this one.
var agentGatewayRoundTripFailureSignatures = []string{
	"`reasoning_content` in the thinking mode",
}

// agentFailureReason returns a clarified failure reason when a failed agent
// job's own folded error names a known cause, and "" otherwise — never guessing
// on a message that does not match one of the tools' own known phrasings.
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

// finishEnvironmentJob records the outcome the supervisor observed. This is the
// only place an exit status is ever produced, and it comes from waiting on the
// process — never from parsing output, and never from a shell's $?.
//
// childPID is the process cmd.Wait just reaped. detachEnvironmentJobChild put
// it in a process group named after its own pid, so a live process still in
// that group after the leader is gone — work the leader backgrounded and
// never waited for — is answered here, not left for the exit code alone to
// misreport as a clean success.
//
// captureAgentJobWorktreeOutcome runs here too, before the exit code alone
// could be read as the whole story: an agent job's own working tree is
// something its exit status says nothing about at all (see job_worktree.go).
//
// reclaimAgentJobWorkClone needs a snapshot that already carries this job's
// own final EnvironmentJobStateExited (or abandoned/gate-incomplete, which it
// refuses) rather than the "running" state the job still carried when
// finishEnvironmentJob was entered (see work_clone_reclaim.go for why that
// ordering matters) -- but it must also run, and any removal it decides on
// must actually finish, before that terminal state is written anywhere a
// caller can observe it. job.Finished() (what await/status poll on) is true
// the instant State reaches a terminal value, so settling State and
// CloneReclaimed/CloneKeptReason in two separate recorder.update calls left a
// window where a poll could read "finished" off the first write while the
// clone was still fully present on disk -- a caller-visible false success,
// not just a stale field. settle is applied to an in-memory copy first (no
// disk write), reclaim is decided and acted on against that settled copy, and
// only then does the single recorder.update below make any of it durable.
func finishEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, writer *jobOutputWriter, childPID int, state *os.ProcessState, waitErr error, adoptedBaseline map[int]struct{}) error {
	// Fold the stream's tail before the outcome lands, so the finished record
	// carries what the run last did rather than the poll's stale view of it.
	beat.refresh(false)

	cancelledBy, wasCancelled := consumeEnvironmentJobCancelRequest(recorder.dir, recorder.snapshot().ID)

	code, signal, reason, jobState, startedJobFailed := resolveEnvironmentJobOutcome(recorder, childPID, state, waitErr, adoptedBaseline)
	// A job that already spent its bounded reinvocations (see
	// considerEnvironmentJobReinvocation) and still ends up here gate-incomplete
	// or naming a StartedJobFailed exhausted its automatic "later" -- say so,
	// rather than reporting the same reason a job that never got a reinvocation
	// at all would report.
	if count := recorder.snapshot().ReinvocationCount; count > 0 && (jobState == shared.EnvironmentJobStateGateIncomplete || startedJobFailed != "") {
		note := fmt.Sprintf("already resumed %d time(s) without a clean outcome; the reinvocation bound is exhausted", count)
		if reason == "" {
			reason = note
		} else {
			reason = reason + " (" + note + ")"
		}
	}
	worktree := captureAgentJobWorktreeOutcome(recorder.snapshot(), recorder.dir)
	// Captured once resolveEnvironmentJobOutcome returns, before the reclaim
	// decision runs, so EndedAt reflects when this job's own outcome was
	// actually settled — which, for a job that waited out one it started (see
	// resolveEnvironmentJobOutcome), can be well after its own process exited
	// — rather than drifting later still by however long reclaim's own git
	// plumbing and removal take.
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

// resolveEnvironmentJobOutcome decides the exit code, signal, reason,
// terminal state, and started-job failure finishEnvironmentJob records. State
// and reason can be overridden by work the job left behind that it never
// waited for: a process still alive in the job's own process group, in its
// session, or reparented onto this supervisor (abandoned) is decided
// immediately, since nothing about a leftover process is worth waiting on. The
// three are widest-last: the group misses work backgrounded into a fresh group
// of its own, the session misses work that called setsid as well, and the
// supervisor's own adopted descendants miss nothing -- see
// environmentJobDescendantSurvivors. Work is only reported as abandoned when
// it is this job's to report at all, which is why the widest scan runs against
// an exclusion set rather than the bare process table (see
// environmentJobLeftoverExclusions). A sibling job record naming this job as its
// StartedByJobID is different: rather than declaring the outcome incomplete
// on the spot, this waits for it (see awaitEnvironmentJobRunningChildren) —
// the whole motivation being that a caller reading this job's own record
// should not have to separately chase down and await what it started merely
// because this job's own process happened to exit first. Only once that wait
// times out does gate-incomplete apply; if it ends because the started job
// finished, its failure (if any) is folded into startedJobFailed instead, so
// a clean exit code from this job's own process never overshadows a real
// failure in work it waited for.
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

// environmentJobLeftoverExclusions names the pids a job's own finish check must
// not count as leftover work it is answerable for: the baseline already
// captured (see environmentJobDescendantBaseline), plus the supervisor of every
// job this one deliberately handed off.
//
// The second half is what keeps handoff meaning what it says. A handed-off job
// is by definition work the parent does not wait for, and it is stored that way
// by the record relationship alone — the child's Handoff field, which
// environmentJobRunningChildren and the failed-child scan both already honor.
// The descendant scan cannot see that: it reads a process table, and the
// handed-off job's supervisor is reparented onto this one as soon as the work
// that started it exits (that supervisor deliberately detached, so it is
// exactly the shape the scan was added to catch). Without this, a job that
// handed work off would report itself abandoned over the very job it said it
// was not waiting for.
//
// Scoped to this job's own children, the same scope every other read of the
// handoff relationship uses: a supervisor reparented onto this process is
// always a descendant of this job's work, and a handed-off job this job did
// not start is not its to reason about.
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

// awaitEnvironmentJobRunningChildren blocks until no non-handoff job started
// by parentID is still running, or cap elapses, returning whatever is still
// running at that point (nil once none are). Unlike AwaitEnvironmentJob, this
// has no caller holding a connection or a terminal open on the other end of
// it — the supervisor calling this is already a detached background process
// — so it polls internally for as long as cap allows instead of returning a
// "still running" answer for someone else to re-ask.
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

// environmentJobGateIncompleteReason names the still-running job(s) a caller
// needs to await for a real outcome, so the reason is actionable rather than
// just a label.
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

// environmentJobFailedChildReason names the job(s) this job started, waited
// for, and that did not succeed, so a caller reading this job's own failure
// knows which started job to look at rather than only that this job's own
// exit code (which can still read as clean) is not the whole story.
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
