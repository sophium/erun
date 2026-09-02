package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A job is an activity lease plus an outcome. The lease already records what is
// keeping an environment busy; what it cannot answer is "did it finish, and with
// what result". Answering that through a general-purpose escape hatch means
// re-implementing detachment, log redirection, polling, and exit-status capture
// in shell for every caller — which is where the observation bugs come from, not
// from the work itself.
//
// Three rules follow from those bugs and shape everything below.
//
// The outcome is captured by the process that ran the work, inside the
// environment, and stored as data. No sentinel token in a log, no exit code
// expanded by whichever shell happened to wrap the call.
//
// Liveness is a probe of a recorded pid, never a match against a command line.
// A pattern can match the process doing the observing — the polling loop, the
// caller's own shell — and two of those bugs were exactly that.
//
// An answer is definite or explicitly unknown, never silently partial. A job
// whose supervisor vanished without recording an outcome reads as unknown; it
// never reads as success, and it never disappears.

const (
	// EnvironmentJobStateRunning means the supervisor is alive and the work has
	// not reported an outcome yet.
	EnvironmentJobStateRunning = "running"
	// EnvironmentJobStateExited means the work finished and its exit status was
	// captured by the supervisor that ran it.
	EnvironmentJobStateExited = "exited"
	// EnvironmentJobStateUnknown means the record outlived whatever was meant to
	// finish it — the pod was replaced, or the supervisor was killed outright.
	// The outcome is not recoverable, and saying so is the point: an orchestrator
	// that cannot tell this from success would act on a result nobody produced.
	EnvironmentJobStateUnknown = "unknown"
	// EnvironmentJobStateAbandoned means the job's own process exited and its
	// exit status was captured, but it left other processes it spawned still
	// running in its process group — background work it started and never
	// waited for, e.g. a gate backgrounded and the job that started it exiting
	// anyway. It is never success, whatever the captured exit code says:
	// something the job started continues unsupervised, and nothing further
	// will ever be reported for it.
	EnvironmentJobStateAbandoned = "abandoned"
	// EnvironmentJobStateGateIncomplete means this job's own process ended while
	// a job it started (StartedByJobID names this job as the parent) was still
	// running — an agent job that ran a gate through its own `job start`, then
	// exited before the gate reached a verdict, is the motivating case. Unlike
	// EnvironmentJobStateAbandoned, the still-running work is not a process in
	// this job's own process group (which a detached job deliberately escapes,
	// by design, so it survives the caller that started it) — it is a sibling
	// record in the same job store. Never success, whatever the captured exit
	// code says: the child job's own outcome has not been observed yet, and
	// nothing about this job's own exit reports it.
	EnvironmentJobStateGateIncomplete = "gate-incomplete"

	// UnknownReasonKind values are what an orchestrator branches on instead of
	// pattern-matching the free-text Reason. Each names a distinct,
	// attributable cause for a job that ended in EnvironmentJobStateUnknown.
	//
	// UnknownReasonPodReplaced is certain, not a guess: the job recorded the
	// hostname of the pod that started it (a Kubernetes pod's hostname is its
	// pod name, unique per pod instance), and the hostname reading this record
	// back does not match. The runtime pod was recreated out from under the
	// work — most often eviction (e.g. a release filling the node's disk) or an
	// operator-triggered redeploy/restart — and the work is gone with it.
	UnknownReasonPodReplaced = "pod-replaced"
	// UnknownReasonContainerRestarted is certain, not a guess: the same pod
	// answered a live `kubectl get pod` with lastState.terminated for the
	// runtime container, naming a reason and exit code that lands at or after
	// this job was last known alive. The pod was not replaced; its container
	// was killed and Kubernetes restarted it in place, taking the supervisor
	// process with it.
	UnknownReasonContainerRestarted = "container-restarted"
	// UnknownReasonSupervisorGone means the same pod is still running, and
	// nothing checkable explains why the supervisor process the job recorded
	// is not: either the Kubernetes API could not be reached to check for a
	// container restart, or it answered and found none attributable to this
	// job. Distinct from a pod replacement (the environment survived) and
	// from a container restart (a checked, named cause) alike -- this is the
	// genuinely-unknown case, and the reason says so instead of guessing.
	UnknownReasonSupervisorGone = "supervisor-gone"
	// UnknownReasonAttachedProcessGone means the job was never started by
	// erun — it tracks a pid the caller named — so there was never a
	// supervisor in a position to observe an exit status, pod replacement or
	// not.
	UnknownReasonAttachedProcessGone = "attached-process-gone"

	// DefaultEnvironmentJobOutputLimitBytes bounds one job's captured output so a
	// chatty run cannot fill the environment's home volume. The outcome never
	// comes from the log, so hitting the cap costs detail, never the result. A
	// long agent run in stream-json mode blows through 4 MiB well under an hour,
	// so the default is wide enough to cover most single-session runs; progress
	// itself no longer depends on this cap at all (agentProgressReader is fed
	// directly from the process's own writes).
	DefaultEnvironmentJobOutputLimitBytes int64 = 16 << 20

	// DefaultEnvironmentJobAwaitTimeout is short enough that a caller polls
	// rather than parks on a connection.
	DefaultEnvironmentJobAwaitTimeout = 30 * time.Second
	// MaxEnvironmentJobAwaitTimeout is the ceiling that makes "bounded" real. A
	// caller that wants to wait longer calls await again; holding one call open
	// for a job's lifetime is the failure mode this surface replaces.
	MaxEnvironmentJobAwaitTimeout = 10 * time.Minute

	// EnvironmentJobRetention keeps a finished job readable long enough for an
	// orchestrator that reconnects — after a dropped transport, a desktop
	// restart, or overnight — to still learn the outcome. Reaping at exit would
	// recreate the problem this closes.
	EnvironmentJobRetention = 24 * time.Hour
	// EnvironmentJobHistoryCap bounds the finished records kept per environment
	// so a caller starting many short jobs cannot grow the store without limit.
	EnvironmentJobHistoryCap = 50

	// EnvironmentJobAliveHeartbeatInterval is the fixed cadence the supervisor
	// stamps LastAliveAt/AliveSeq at, independent of the activity lease's much
	// coarser renewal interval (300s at the default TTL) and of an agent job's
	// 2s progress fold. It does not depend on the work producing output, which
	// is what lets it answer "is the supervisor still there" for a command job
	// that is legitimately silent for minutes (an image pull, a slow test).
	EnvironmentJobAliveHeartbeatInterval = 1 * time.Second
	// EnvironmentJobAliveStaleMs is the documented caller rule: once AliveAgeMs
	// exceeds this, stop waiting and treat the job as failed with an unknown
	// outcome. It is 5x EnvironmentJobAliveHeartbeatInterval — headroom for poll
	// jitter and scheduling delay, not for the beat itself ever being late by
	// design.
	EnvironmentJobAliveStaleMs int64 = 5000

	// DefaultEnvironmentJobOutputReadBytes bounds one output read so a poll
	// returns a page rather than the whole log.
	DefaultEnvironmentJobOutputReadBytes int64 = 64 << 10

	environmentJobDirName     = "jobs"
	environmentJobLeasePrefix = "job-"

	// environmentJobIDEnvVar is the env var a job's own supervisor sets on the
	// work's process, naming the job the work is running as. Anything the work
	// spawns inherits it the same way it inherits any other environment
	// variable, so a nested `job start` run from inside it (agent-gate.sh's
	// detach-and-await, or an agent's own Bash tool) reads it back and records
	// StartedByJobID without either side threading it through explicitly.
	environmentJobIDEnvVar = "ERUN_JOB_ID"

	// EnvironmentJobGateIncompleteWaitCapEnvVar overrides
	// environmentJobGateIncompleteWaitCap for a single job supervisor process,
	// parsed with time.ParseDuration; an empty or unparseable value falls back
	// to the default. It exists because the default is generous on purpose
	// (see environmentJobGateIncompleteWaitCap) and a subprocess-level
	// integration test has no in-process var to swap the way a Go unit test
	// does -- the same shape as ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES tuning a
	// different generous-by-default floor.
	EnvironmentJobGateIncompleteWaitCapEnvVar = "ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP"
	// EnvironmentJobGateIncompletePollEnvVar is EnvironmentJobGateIncompleteWaitCapEnvVar's
	// twin for the poll interval, same parsing and fallback rule.
	EnvironmentJobGateIncompletePollEnvVar = "ERUN_JOB_GATE_INCOMPLETE_POLL"
)

// environmentJobGateIncompleteWaitCap bounds how long a job's own finish check
// waits for a non-handoff job it started to reach a verdict before giving up
// and recording gate-incomplete anyway. It is generous by design: unlike an
// interactive `job await` call, nothing is holding a connection or a caller's
// turn open here — the supervisor is already a detached background process,
// so waiting out even the longest gate costs nothing the await ceiling above
// exists to avoid. It shares EnvironmentJobRetention's value only because both
// answer "how long is worth waiting before giving up", not because the two
// concerns are the same one. A var, not a const, so a Go test can shrink it
// directly rather than genuinely waiting out a seeded "still running" child;
// resolveEnvironmentJobGateIncompleteWaitCap is what a subprocess (an
// integration test, or an operator) overrides instead, via
// EnvironmentJobGateIncompleteWaitCapEnvVar.
var environmentJobGateIncompleteWaitCap = EnvironmentJobRetention

// environmentJobGateIncompletePoll is how often the wait re-reads the started
// job's record while it waits. A var for the same test-shrinking reason as
// environmentJobGateIncompleteWaitCap above.
var environmentJobGateIncompletePoll = 2 * time.Second

// resolveEnvironmentJobGateIncompleteWaitCap is what resolveEnvironmentJobOutcome
// actually waits by: EnvironmentJobGateIncompleteWaitCapEnvVar when it is set
// to a valid positive duration, otherwise environmentJobGateIncompleteWaitCap.
func resolveEnvironmentJobGateIncompleteWaitCap() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompleteWaitCapEnvVar, environmentJobGateIncompleteWaitCap)
}

// resolveEnvironmentJobGateIncompletePoll is environmentJobGateIncompletePoll's
// EnvironmentJobGateIncompletePollEnvVar-aware twin.
func resolveEnvironmentJobGateIncompletePoll() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompletePollEnvVar, environmentJobGateIncompletePoll)
}

// environmentJobMaxReinvocations bounds how many bounded follow-up turns an
// agent job's own supervisor will run in response to the job it started coming
// back incomplete or failed (see decideEnvironmentJobReinvocation). It is small
// on purpose: this is a safety-bounded retry of a scoped, already-observed
// failure, not a general "keep trying" loop, and each turn is a real LLM
// invocation with its own cost and its own chance of causing something new. A
// var so a test can shrink it without genuinely burning reinvocations; an
// operator overrides via EnvironmentJobMaxReinvocationsEnvVar the same way
// resolveEnvironmentJobGateIncompleteWaitCap works.
var environmentJobMaxReinvocations = 2

// environmentJobReinvocationBudget caps the wall-clock time a chain of
// reinvocations may spend in total, independently of environmentJobMaxReinvocations:
// a count cap alone does not bound cost if the model can spend an unbounded
// amount of time (and, on some providers, budget) inside each turn. The two
// bounds are deliberately different shapes -- one bounds how many extra
// inferences can run, the other bounds how long the whole chain may keep the
// environment busy -- so either one exhausting stops the chain.
var environmentJobReinvocationBudget = 30 * time.Minute

const (
	// EnvironmentJobMaxReinvocationsEnvVar overrides environmentJobMaxReinvocations
	// for a single job supervisor process; an empty or unparseable (non-integer,
	// or negative) value falls back to the default. Same shape as
	// EnvironmentJobGateIncompleteWaitCapEnvVar, for the same reason: an
	// integration test needs to shrink or grow this without a package-level Go
	// test's ability to swap the var directly.
	EnvironmentJobMaxReinvocationsEnvVar = "ERUN_JOB_MAX_REINVOCATIONS"
	// EnvironmentJobReinvocationBudgetEnvVar is EnvironmentJobMaxReinvocationsEnvVar's
	// twin for the wall-clock budget, parsed with time.ParseDuration.
	EnvironmentJobReinvocationBudgetEnvVar = "ERUN_JOB_REINVOCATION_BUDGET"
)

// EnvironmentJobMaxReinvocations returns the bound a caller renders alongside
// EnvironmentJob.ReinvocationCount (e.g. `job status`'s "resumed N/M" suffix),
// so the displayed bound can never drift from the one actually enforced.
func EnvironmentJobMaxReinvocations() int {
	return resolveEnvironmentJobMaxReinvocations()
}

// resolveEnvironmentJobMaxReinvocations is environmentJobMaxReinvocations,
// EnvironmentJobMaxReinvocationsEnvVar-aware.
func resolveEnvironmentJobMaxReinvocations() int {
	if raw := strings.TrimSpace(os.Getenv(EnvironmentJobMaxReinvocationsEnvVar)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return environmentJobMaxReinvocations
}

// resolveEnvironmentJobReinvocationBudget is environmentJobReinvocationBudget's
// EnvironmentJobReinvocationBudgetEnvVar-aware twin.
func resolveEnvironmentJobReinvocationBudget() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobReinvocationBudgetEnvVar, environmentJobReinvocationBudget)
}

func resolveEnvironmentJobDurationOverride(envVar string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envVar)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// CurrentEnvironmentJobID returns this process's own job id -- the job whose
// supervisor set ERUN_JOB_ID on it -- or "" outside any job. registerEnvironmentJob
// reads this same env var directly for a job started as a plain nested
// subprocess (agent-gate.sh, an agent's own Bash tool), where the inheritance
// happens for free. It is exported for a caller that cannot rely on that
// inheritance: forwarding a job-start request through the MCP edge crosses
// into that server's own long-lived process, which was never itself started as
// anyone's job and so has no ERUN_JOB_ID to inherit from, however deep the
// logical nesting on the calling side. Such a caller reads its own id here and
// threads it explicitly as StartEnvironmentJobParams.StartedByJobID.
func CurrentEnvironmentJobID() string {
	return strings.TrimSpace(os.Getenv(environmentJobIDEnvVar))
}

// EnvironmentJob is one unit of long work and its outcome.
type EnvironmentJob struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is running, exited, or unknown. It is resolved on every read against
	// the supervisor's liveness, so a record can never claim to be running after
	// the process behind it is gone.
	State string `json:"state"`
	// Succeeded is the one field that answers "did this actually work",
	// computed fresh on every read exactly like AliveAgeMs so it can never lag
	// what State/ExitCode/WorktreeDirty actually say. It exists because those
	// three fields do not, on their own, rule out false success: ExitCode can
	// be a clean 0 while State is abandoned or gate-incomplete (the process
	// that ended cleanly is not the same claim as the work it started
	// reaching a real outcome), or while WorktreeDirty says the agent's own
	// changes were never committed. A caller — especially a JSON/MCP one that
	// cannot call the Go helper this mirrors — checks this field alone rather
	// than re-deriving the combination itself.
	Succeeded bool `json:"succeeded"`
	// Kind is command or agent. An agent job runs an AI tool in its streaming
	// mode, which is what lets it report progress rather than only a state.
	Kind string `json:"kind,omitempty"`
	// AgentTool names the AI tool an agent job runs, and is empty otherwise.
	AgentTool string `json:"agentTool,omitempty"`
	// Progress is the normalized view of an agent run, folded from the tool's
	// event stream by the supervisor. It is nil for a command job and for an
	// agent run that has not emitted yet — never a made-up zero state.
	Progress *AgentJobProgress `json:"progress,omitempty"`
	// Command is the argv the supervisor ran. Empty for an attached job, and
	// for a task job — that one is a Go call in this process, not a subprocess,
	// so there is no argv to record and synthesising one would assert a command
	// nobody ran. What it did is in Name, LogPath, and Result instead.
	Command []string `json:"command,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	// PID is the supervisor for a started job, and the caller's own process for
	// an attached one. It is the only thing liveness is ever decided by.
	PID int `json:"pid,omitempty"`
	// ChildPID is the work itself, the process cancel signals. Recording it
	// separately is what lets a cancel reach the work without touching the
	// bookkeeping that has to survive to report the outcome.
	ChildPID  int       `json:"childPid,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
	// ExitCode is set for every job that reached exited or abandoned, and is -1
	// when the work was terminated by a signal. It is nil in every other state,
	// so a missing outcome can never be read as a zero one.
	ExitCode *int `json:"exitCode"`
	// Signal names the signal that ended the work, empty when it exited normally.
	Signal string `json:"signal,omitempty"`
	// Reason explains a non-obvious state: why the outcome is unknown, or how the
	// work ended when it was not a plain exit.
	Reason string `json:"reason,omitempty"`
	// UnknownReasonKind is Reason's machine-readable twin, set only when State is
	// unknown: one of UnknownReasonPodReplaced, UnknownReasonContainerRestarted,
	// UnknownReasonSupervisorGone, or UnknownReasonAttachedProcessGone. A caller
	// branches on this rather than pattern-matching Reason's free text.
	UnknownReasonKind string `json:"unknownReasonKind,omitempty"`
	// Attached marks work erun did not start. Its outcome is never captured,
	// because nothing erun ran was in a position to observe it.
	Attached bool `json:"attached,omitempty"`
	// Hostname is the pod hostname the supervisor observed at start (empty for
	// an attached job). A Kubernetes pod's hostname is its pod name, unique per
	// pod instance, so a later read from a different hostname is definitive
	// proof the runtime pod was replaced — not a guess.
	Hostname string `json:"hostname,omitempty"`
	// StartedByJobID names the job whose own process was running when this job
	// was started (the parent's ERUN_JOB_ID, inherited down whatever it
	// spawned), empty when this job was started from outside any other job.
	// It is what lets the parent's own finish check tell "a job I started" from
	// "an unrelated job that happens to share this environment". Work reaching
	// this environment through its MCP edge has nothing to inherit from — that
	// server was never started as anyone's job — so the caller supplies the
	// value explicitly there; an absent one stays absent rather than being
	// guessed from whichever job happens to be running here.
	StartedByJobID string `json:"startedByJobId,omitempty"`
	// Handoff marks this job as deliberately meant to outlive whatever started
	// it, set by `job start --handoff`. Without it, a parent whose own process
	// exits while this job is still running waits for it (see
	// EnvironmentJobStateGateIncomplete) rather than trusting its own exit
	// code — the right default for a nested gate, and the wrong one for work an
	// agent starts and intentionally leaves running past its own turn (a
	// release, a long render). Handoff is how a caller tells the two apart:
	// this job is excluded from its parent's own finish check entirely, both
	// while it runs and once it is done.
	Handoff bool `json:"handoff,omitempty"`

	LogPath          string `json:"logPath,omitempty"`
	OutputBytes      int64  `json:"outputBytes"`
	OutputLimitBytes int64  `json:"outputLimitBytes,omitempty"`
	// OutputTruncated says the cap was reached and later output was dropped, so a
	// short log is never mistaken for a quiet run.
	OutputTruncated bool `json:"outputTruncated,omitempty"`

	// LeaseID is the activity lease the job holds for its lifetime, which is why
	// starting one also makes the environment read as busy.
	LeaseID string `json:"leaseId,omitempty"`

	// LastAliveAt is the pod-clock timestamp of the supervisor's last alive
	// beat, stamped on a fixed cadence independent of the work's own output
	// (see EnvironmentJobAliveHeartbeatInterval). It is never compared against
	// a caller's own clock — the two clocks are not the same clock — which is
	// why every reader derives AliveAgeMs from it instead of exposing it raw
	// for a caller to subtract against.
	LastAliveAt time.Time `json:"lastAliveAt,omitempty"`
	// AliveSeq is a monotonic counter bumped on every beat, so a caller can
	// tell "still beating" from "the same timestamp read twice".
	AliveSeq int64 `json:"aliveSeq,omitempty"`
	// AliveAgeMs is computed fresh on every read as now-LastAliveAt, in the
	// pod's own clock, and is nil only when the job has never beaten (an
	// attached job, or one whose supervisor has not registered yet). A caller
	// that sees this exceed EnvironmentJobAliveStaleMs treats the job as
	// failed — reported as an unknown outcome, never as success and never as
	// a tool error — rather than waiting on a beat a dead supervisor can no
	// longer produce.
	AliveAgeMs *int64 `json:"aliveAgeMs,omitempty"`

	// Result is a task job's typed return value, captured verbatim as JSON once
	// it exits. It is never flattened into a log a caller has to parse back
	// into shape: whatever a task returns is what a caller reads back here, in
	// the same structure it would have gotten from calling the work
	// synchronously. Nil for every other kind, and for a task that has not
	// finished yet.
	Result json.RawMessage `json:"result,omitempty"`

	// WorktreeDirty is set only for a finished agent job whose Dir was a git
	// working tree that still had uncommitted changes (tracked or untracked,
	// respecting .gitignore) when the job ended. False for every command job,
	// for an agent job with no working directory or one outside a git repo,
	// and for an agent job that left its tree clean — none of those cases are
	// distinguishable from each other, and none of them need to be: this field
	// exists only to make "the agent's turn ended with unsaved work" visible,
	// which an exit code alone cannot say. See job_worktree.go.
	WorktreeDirty bool `json:"worktreeDirty,omitempty"`
	// WorktreeBranch is the branch HEAD pointed at when the check ran, "HEAD"
	// literal when detached, empty when WorktreeDirty is false.
	WorktreeBranch string `json:"worktreeBranch,omitempty"`
	// WorktreeDetached is true when HEAD was not on a branch at all. A
	// checkpoint commit there would be unreachable the moment HEAD moves, so
	// the supervisor never attempts one in that state.
	WorktreeDetached bool `json:"worktreeDetached,omitempty"`
	// WorktreeCommit is the machine-authored checkpoint commit the supervisor
	// made to preserve a dirty tree, empty when it made none — whether because
	// the tree was clean, committing was refused as unsafe, or the commit
	// itself failed (see WorktreeReason for which).
	WorktreeCommit string `json:"worktreeCommit,omitempty"`
	// WorktreePushed reports whether WorktreeCommit reached WorktreeRemote. A
	// commit that exists only in this working tree is exactly as exposed to a
	// lost pod as the uncommitted changes it was meant to save.
	WorktreePushed bool `json:"worktreePushed,omitempty"`
	// WorktreeRemote is the remote WorktreeCommit was pushed to, empty when
	// nothing was pushed.
	WorktreeRemote string `json:"worktreeRemote,omitempty"`
	// WorktreeReason explains a dirty tree the supervisor could not fully
	// resolve: why no checkpoint commit was made, or why one was made but not
	// pushed. Empty when WorktreeDirty is false, or when a commit was made and
	// pushed cleanly.
	WorktreeReason string `json:"worktreeReason,omitempty"`

	// CloneReclaimed reports whether the supervisor removed this agent job's
	// own Dir after it finished, because its working tree was clean and every
	// commit reachable from HEAD was already reachable from a remote. False
	// for a command job, a job that did not finish cleanly (see
	// EnvironmentJobStateExited), a job whose Dir sat outside the work root,
	// or one the supervisor judged unsafe to remove (see CloneKeptReason).
	// See work_clone_reclaim.go.
	CloneReclaimed bool `json:"cloneReclaimed,omitempty"`
	// CloneKeptReason explains why an agent job's Dir was left in place: a
	// dirty tree, unpushed commits with no proof they landed elsewhere under
	// a different commit, a detached HEAD that is not provably pushed, or no
	// upstream at all. Empty when CloneReclaimed is true, or when the job was
	// never a candidate for reclaim (a command job, one outside the work
	// root, or one that did not finish cleanly).
	CloneKeptReason string `json:"cloneKeptReason,omitempty"`

	// StartedJobFailed names a job this job started (StartedByJobID, excluding
	// any marked Handoff) that had already finished without succeeding by the
	// time this job's own outcome was recorded. It exists because a clean exit
	// code from this job's own process is not the same claim as a clean
	// outcome from work it waited for (see EnvironmentJobStateGateIncomplete):
	// once that wait ends because the started job finished, its own failure
	// would otherwise vanish behind this job's own exit code. Empty when every
	// non-handoff job this job started succeeded, or when it started none.
	StartedJobFailed string `json:"startedJobFailed,omitempty"`

	// ReinvocationCount is how many bounded follow-up turns an agent job's own
	// supervisor has already run because the job it started had not reached a
	// verdict, or had reached a bad one, by the time this job's own process
	// exited (see decideEnvironmentJobReinvocation in job_supervisor.go). It is
	// what makes the bound real to a caller rather than only to the code: a job
	// stuck at EnvironmentJobMaxReinvocations here, still gate-incomplete or
	// still naming a StartedJobFailed, has exhausted its automatic "later" and
	// needs a human or an external caller to act. Zero for a command job and
	// for an agent job that never needed one.
	ReinvocationCount int `json:"reinvocationCount,omitempty"`
}

// Finished reports whether the job reached a terminal state.
func (j EnvironmentJob) Finished() bool {
	return j.State == EnvironmentJobStateExited || j.State == EnvironmentJobStateUnknown ||
		j.State == EnvironmentJobStateAbandoned || j.State == EnvironmentJobStateGateIncomplete
}

// environmentJobSucceeded is the only definition of success: an outcome was
// captured and it was zero, with nothing left running behind it and nothing
// left uncommitted in its own working tree. An unknown job is never a
// success, and neither is an abandoned or gate-incomplete one — a zero exit
// code from the process that started work it never waited for, whether that
// work is an unreaped process in its own group or a sibling job record, is
// not the same claim as a zero exit code from a job that finished cleanly. A
// dirty working tree is the same shape of not-actually-clean finish: whatever
// the supervisor managed to preserve on the agent's behalf (see
// job_worktree.go), the agent itself did not commit its own work, and that is
// worth a caller's attention even when everything else about the run looks
// fine. StartedJobFailed is the same shape again, one step removed: a job
// this job started and waited for reached a verdict, and the verdict was not
// success. Backs the EnvironmentJob.Succeeded field.
func environmentJobSucceeded(j EnvironmentJob) bool {
	return j.State == EnvironmentJobStateExited && j.ExitCode != nil && *j.ExitCode == 0 &&
		!j.WorktreeDirty && j.StartedJobFailed == ""
}

// LoadEnvironmentJob returns one job with its state resolved, reconciling and
// persisting an abandoned record on the way out.
func LoadEnvironmentJob(tenant, environment, id string, now time.Time) (EnvironmentJob, error) {
	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		return EnvironmentJob{}, err
	}
	resolved, err := resolveEnvironmentJobID(id)
	if err != nil {
		return EnvironmentJob{}, err
	}
	job, err := readEnvironmentJob(filepath.Join(dir, resolved+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return EnvironmentJob{}, fmt.Errorf("no job %q in %s/%s", resolved, tenant, environment)
		}
		return EnvironmentJob{}, err
	}
	return reconcileEnvironmentJob(dir, job, normalizeJobNow(now), processAlive, currentJobHostname()), nil
}

// LoadEnvironmentJobs returns every retained job, newest first, pruning records
// that aged out as it reads.
func LoadEnvironmentJobs(tenant, environment string, now time.Time) ([]EnvironmentJob, error) {
	return loadEnvironmentJobs(tenant, environment, normalizeJobNow(now), processAlive, currentJobHostname())
}

func loadEnvironmentJobs(tenant, environment string, now time.Time, alive func(int) bool, hostname string) ([]EnvironmentJob, error) {
	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]EnvironmentJob, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := readEnvironmentJob(filepath.Join(dir, entry.Name()))
		if err != nil {
			// A record we cannot read cannot answer anything, and leaving it would
			// keep an unreadable job in every listing forever.
			removeEnvironmentJobFiles(dir, strings.TrimSuffix(entry.Name(), ".json"))
			continue
		}
		jobs = append(jobs, reconcileEnvironmentJob(dir, job, now, alive, hostname))
	}
	jobs = pruneEnvironmentJobs(dir, jobs, now)
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].StartedAt.Equal(jobs[j].StartedAt) {
			return jobs[i].StartedAt.After(jobs[j].StartedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs, nil
}

// reconcileEnvironmentJob is the whole liveness rule. A record that still claims
// to be running is only believed while the process it named is alive; otherwise
// the job is demoted to unknown and that demotion is persisted, so every later
// read gives the same definite answer.
func reconcileEnvironmentJob(dir string, job EnvironmentJob, now time.Time, alive func(int) bool, hostname string) EnvironmentJob {
	return reconcileEnvironmentJobWithRestartCheck(dir, job, now, alive, hostname, runOpenKubectl)
}

// reconcileEnvironmentJobWithRestartCheck is reconcileEnvironmentJob with the
// Kubernetes restart lookup injectable, so a test can supply a fake pod
// response instead of shelling out to a real kubectl.
func reconcileEnvironmentJobWithRestartCheck(dir string, job EnvironmentJob, now time.Time, alive func(int) bool, hostname string, restartRunner openKubectlRunnerFunc) EnvironmentJob {
	// Computed fresh on every read, in the reader's own now, and never
	// persisted: a stale value on disk would be meaningless without the read
	// time that produced it, exactly like OutputBytes below.
	job.AliveAgeMs = environmentJobAliveAgeMs(job.LastAliveAt, now)
	if job.State != EnvironmentJobStateRunning {
		job.Succeeded = environmentJobSucceeded(job)
		return job
	}
	if job.PID > 0 && alive != nil && alive(job.PID) {
		job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
		job.Succeeded = environmentJobSucceeded(job)
		return job
	}
	job = demoteEnvironmentJobToUnknown(job, now, hostname, restartRunner)
	job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
	job.Succeeded = environmentJobSucceeded(job)
	_ = writeEnvironmentJob(dir, job)
	return job
}

// demoteEnvironmentJobToUnknown fills in the State/UnknownReasonKind/Reason a
// job gets once its supervisor is confirmed gone: attached work was never in
// a position to observe an exit, a hostname mismatch is a definite pod
// replacement, a hostname match rules that out and defers to
// reconcileSamePodUnknownReason, and no hostname at all leaves nothing to
// compare against.
func demoteEnvironmentJobToUnknown(job EnvironmentJob, now time.Time, hostname string, restartRunner openKubectlRunnerFunc) EnvironmentJob {
	job.State = EnvironmentJobStateUnknown
	job.EndedAt = now
	job.ExitCode = nil
	switch {
	case job.Attached:
		job.UnknownReasonKind = UnknownReasonAttachedProcessGone
		job.Reason = fmt.Sprintf("attached process %d is gone; erun did not run this work, so no exit status was recorded", job.PID)
	case job.Hostname != "" && job.Hostname != hostname:
		job.UnknownReasonKind = UnknownReasonPodReplaced
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; the runtime pod was replaced (started on %q, this is %q)", job.PID, job.Hostname, hostname)
	case job.Hostname != "" && job.Hostname == hostname:
		job.UnknownReasonKind, job.Reason = reconcileSamePodUnknownReason(job, hostname, restartRunner)
	default:
		// This job predates hostname tracking, or hostname resolution failed on
		// both ends: there is nothing to compare against, so neither a
		// replacement nor a restart can be confirmed or ruled out.
		job.UnknownReasonKind = UnknownReasonSupervisorGone
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; this job predates hostname tracking, so whether the pod was replaced could not be determined", job.PID)
	}
	return job
}

// reconcileSamePodUnknownReason handles the one case where a replacement is
// ruled out (hostname matches, so this is definitely the same pod): it asks
// Kubernetes whether the runtime container itself restarted, and only credits
// that restart when its timestamp lands at or after this job was last known
// alive -- a checked, named cause beats a guessed one, but only when it is
// actually attributable to this job rather than some earlier, unrelated one.
func reconcileSamePodUnknownReason(job EnvironmentJob, hostname string, restartRunner openKubectlRunnerFunc) (kind, reason string) {
	restarted, terminatedReason, exitCode, finishedAt, ok := jobSupervisorContainerRestart(hostname, DevopsComponentName, restartRunner)
	if !ok || !restarted || finishedAt.Before(jobLastKnownAliveAt(job)) {
		return UnknownReasonSupervisorGone, fmt.Sprintf("job supervisor %d is gone without recording an exit status; the pod was not replaced (same hostname), and why the supervisor process ended could not be determined", job.PID)
	}
	if terminatedReason == "" {
		terminatedReason = "no reason reported"
	}
	return UnknownReasonContainerRestarted, fmt.Sprintf("job supervisor %d is gone without recording an exit status; the %s container restarted (%s, exit code %d) at %s -- the pod itself was not replaced", job.PID, DevopsComponentName, terminatedReason, exitCode, finishedAt.UTC().Format(time.RFC3339))
}

// environmentJobChildren returns every job in dir that names parentID as its
// StartedByJobID, reconciled against current process liveness the same way
// every other read is — so a child whose own supervisor died without
// recording an outcome reads as unknown here too, not as still running. Both
// environmentJobRunningChildren and environmentJobFailedChildren derive from
// this one listing so they agree on what counts as "a job I started" without
// needing the parent to have tracked its children itself.
func environmentJobChildren(dir, parentID string, now time.Time) []EnvironmentJob {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	hostname := currentJobHostname()
	var children []EnvironmentJob
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := readEnvironmentJob(filepath.Join(dir, entry.Name()))
		if err != nil || job.StartedByJobID != parentID {
			continue
		}
		children = append(children, reconcileEnvironmentJob(dir, job, now, processAlive, hostname))
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
	return children
}

// environmentJobRunningChildren returns parentID's children that are still not
// finished, excluding any marked Handoff: a deliberately handed-off job is
// meant to outlive whatever started it, so it must never be what keeps a
// parent's own finish check waiting. This is what lets a job's own finish
// check tell "work I started is still going" from a plain directory listing.
func environmentJobRunningChildren(dir, parentID string, now time.Time) []EnvironmentJob {
	var running []EnvironmentJob
	for _, child := range environmentJobChildren(dir, parentID, now) {
		if child.Handoff {
			continue
		}
		if !child.Finished() {
			running = append(running, child)
		}
	}
	return running
}

// environmentJobFailedChildren returns parentID's children that have finished
// without succeeding, excluding any marked Handoff, and excluding a failure a
// later same-named attempt has since superseded. It is what lets a parent
// whose own process exited cleanly still report the truth once it has waited
// out a job it started (see EnvironmentJobStateGateIncomplete) and that job
// turns out not to have succeeded — a clean exit code from the parent's own
// process is not the same claim as a clean outcome from work it waited for.
//
// The supersede rule matters because a name is not a one-shot id: agent-gate.sh
// folds the tree and command into --id, so an agent that fixes what a gate
// found and reruns it gets a fresh id under the same --name, and the earlier
// failing attempt's record is never replaced (reserveEnvironmentJobID only
// replaces an id that is reused outright). Without this, a parent would name
// that stale failure forever — including once a later attempt under the same
// name went green, reporting exited/succeeded next to a startedJobFailed
// naming a different job than the one that had actually passed.
func environmentJobFailedChildren(dir, parentID string, now time.Time) []EnvironmentJob {
	children := environmentJobChildren(dir, parentID, now)
	latestStartByName := make(map[string]time.Time, len(children))
	for _, child := range children {
		if child.Name == "" {
			continue
		}
		if child.StartedAt.After(latestStartByName[child.Name]) {
			latestStartByName[child.Name] = child.StartedAt
		}
	}
	var failed []EnvironmentJob
	for _, child := range children {
		if child.Handoff || !child.Finished() || child.Succeeded {
			continue
		}
		if child.Name != "" && child.StartedAt.Before(latestStartByName[child.Name]) {
			continue
		}
		failed = append(failed, child)
	}
	return failed
}

// environmentJobAliveAgeMs is nil only when the job has never beaten, so a
// caller never mistakes "no signal yet" for "beating zero milliseconds ago".
func environmentJobAliveAgeMs(lastAliveAt, now time.Time) *int64 {
	if lastAliveAt.IsZero() {
		return nil
	}
	age := now.Sub(lastAliveAt)
	if age < 0 {
		age = 0
	}
	ms := age.Milliseconds()
	return &ms
}

// currentJobHostname is the pod hostname a job supervisor stamps at start and
// compares against at reconcile. Empty on error rather than failing the
// caller: a job record's Hostname is then left unset, and reconciliation
// falls back to the older "most likely replaced" guess instead of
// hard-failing on it.
func currentJobHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// pruneEnvironmentJobs enforces retention: a finished job stays readable for
// EnvironmentJobRetention so a reconnecting caller can still learn its outcome,
// and only the newest EnvironmentJobHistoryCap finished records are kept.
// Running jobs are never pruned.
func pruneEnvironmentJobs(dir string, jobs []EnvironmentJob, now time.Time) []EnvironmentJob {
	kept := make([]EnvironmentJob, 0, len(jobs))
	finished := make([]EnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		if !job.Finished() {
			kept = append(kept, job)
			continue
		}
		if !job.EndedAt.IsZero() && now.Sub(job.EndedAt) >= EnvironmentJobRetention {
			removeEnvironmentJobFiles(dir, job.ID)
			continue
		}
		finished = append(finished, job)
	}
	sort.Slice(finished, func(i, j int) bool {
		if !finished[i].EndedAt.Equal(finished[j].EndedAt) {
			return finished[i].EndedAt.After(finished[j].EndedAt)
		}
		return finished[i].ID < finished[j].ID
	})
	for index, job := range finished {
		if index >= EnvironmentJobHistoryCap {
			removeEnvironmentJobFiles(dir, job.ID)
			continue
		}
		kept = append(kept, job)
	}
	return kept
}

// AwaitEnvironmentJobParams bounds one wait. The timeout is the contract: the
// call returns either an outcome or "still running", never a held connection.
type AwaitEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Timeout     time.Duration
	// Poll is how often the record is re-read; zero takes the default.
	Poll time.Duration
}

// AwaitEnvironmentJobResult separates "not finished yet" from every outcome, so
// a caller never has to infer one from the other.
type AwaitEnvironmentJobResult struct {
	Job EnvironmentJob `json:"job"`
	// TimedOut is true only when the bounded wait elapsed with the job still
	// running. It is never true for a job that failed.
	TimedOut       bool  `json:"timedOut"`
	WaitedSeconds  int64 `json:"waitedSeconds"`
	TimeoutSeconds int64 `json:"timeoutSeconds"`
}

// AwaitEnvironmentJob waits for a job to reach a terminal state, bounded by the
// caller's timeout.
func AwaitEnvironmentJob(params AwaitEnvironmentJobParams) (AwaitEnvironmentJobResult, error) {
	timeout, err := normalizeEnvironmentJobAwaitTimeout(params.Timeout)
	if err != nil {
		return AwaitEnvironmentJobResult{}, err
	}
	poll := params.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	if poll > timeout {
		poll = timeout
	}
	started := time.Now()
	deadline := started.Add(timeout)
	result := AwaitEnvironmentJobResult{TimeoutSeconds: int64(timeout / time.Second)}
	for {
		job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
		if err != nil {
			return AwaitEnvironmentJobResult{}, err
		}
		result.Job = job
		result.WaitedSeconds = int64(time.Since(started) / time.Second)
		if job.Finished() {
			return result, nil
		}
		if !time.Now().Before(deadline) {
			result.TimedOut = true
			return result, nil
		}
		remaining := time.Until(deadline)
		if remaining > poll {
			remaining = poll
		}
		time.Sleep(remaining)
	}
}

func normalizeEnvironmentJobAwaitTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout == 0 {
		return DefaultEnvironmentJobAwaitTimeout, nil
	}
	if timeout < 0 {
		return 0, fmt.Errorf("await timeout must be greater than zero")
	}
	if timeout > MaxEnvironmentJobAwaitTimeout {
		return 0, fmt.Errorf("await timeout %s exceeds the %s ceiling; poll again instead of holding one call open", timeout, MaxEnvironmentJobAwaitTimeout)
	}
	return timeout, nil
}

// ReadEnvironmentJobOutputParams pages through a job's captured output. Offset
// plus NextOffset is what makes progress readable while the work runs.
type ReadEnvironmentJobOutputParams struct {
	Tenant      string
	Environment string
	ID          string
	Offset      int64
	MaxBytes    int64
}

// EnvironmentJobOutput is one page of a job's merged stdout and stderr.
type EnvironmentJobOutput struct {
	Job    EnvironmentJob `json:"job"`
	Offset int64          `json:"offset"`
	// NextOffset is where the next read should start.
	NextOffset int64  `json:"nextOffset"`
	Output     string `json:"output"`
	// HasMore says bytes remain past this page right now.
	HasMore bool `json:"hasMore"`
	// Complete is true only when the job is finished and this page reached the
	// end of its output, so a caller knows it has read everything there will be.
	Complete bool `json:"complete"`
}

// ReadEnvironmentJobOutput returns one page of a job's output. It reads the log
// as it stands, so progress is visible long before the work exits.
func ReadEnvironmentJobOutput(params ReadEnvironmentJobOutputParams) (EnvironmentJobOutput, error) {
	job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
	if err != nil {
		return EnvironmentJobOutput{}, err
	}
	offset := params.Offset
	if offset < 0 {
		return EnvironmentJobOutput{}, fmt.Errorf("output offset must not be negative")
	}
	maxBytes := params.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultEnvironmentJobOutputReadBytes
	}
	result := EnvironmentJobOutput{Job: job, Offset: offset, NextOffset: offset}
	// A job with no log yet — an attached job that named none, or work whose
	// supervisor never got as far as creating one — has nothing to serve, and
	// says so as an empty page rather than as an error.
	if strings.TrimSpace(job.LogPath) == "" {
		result.Complete = job.Finished()
		return result, nil
	}
	page, size, err := readEnvironmentJobLogPage(job.LogPath, offset, maxBytes)
	if err != nil {
		if os.IsNotExist(err) {
			result.Complete = job.Finished()
			return result, nil
		}
		return EnvironmentJobOutput{}, err
	}
	result.Output = string(page)
	result.NextOffset = offset + int64(len(page))
	result.HasMore = result.NextOffset < size
	result.Complete = job.Finished() && !result.HasMore
	return result, nil
}

// readEnvironmentJobLogPage returns at most maxBytes from offset plus the log's
// size at read time, which is what tells a caller whether more has arrived.
func readEnvironmentJobLogPage(path string, offset, maxBytes int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	if offset > size {
		return nil, 0, fmt.Errorf("output offset %d is past the end of the job log (%d bytes)", offset, size)
	}
	read := size - offset
	if read > maxBytes {
		read = maxBytes
	}
	buffer := make([]byte, read)
	if read > 0 {
		if _, err := file.ReadAt(buffer, offset); err != nil {
			return nil, 0, err
		}
	}
	return buffer, size, nil
}

func environmentJobDir(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentJobDirName), nil
}

func resolveEnvironmentJobID(id string) (string, error) {
	value := strings.TrimSpace(id)
	if value == "" {
		return "", fmt.Errorf("job id is required")
	}
	sanitized := sanitizeForFilename(value)
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		return "", fmt.Errorf("job id %q has no usable characters", value)
	}
	return sanitized, nil
}

func readEnvironmentJob(path string) (EnvironmentJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvironmentJob{}, err
	}
	var job EnvironmentJob
	if err := json.Unmarshal(data, &job); err != nil {
		return EnvironmentJob{}, err
	}
	if strings.TrimSpace(job.ID) == "" {
		return EnvironmentJob{}, fmt.Errorf("job record %s has no id", path)
	}
	return job, nil
}

// writeEnvironmentJob replaces the record atomically, because a status read can
// land between the supervisor's write and its rename; a half-written record
// would be exactly the silently-partial answer this store refuses to give.
func writeEnvironmentJob(dir string, job EnvironmentJob) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, job.ID+".json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func removeEnvironmentJobFiles(dir, id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, id+".json"))
	_ = os.Remove(filepath.Join(dir, id+".log"))
}

func environmentJobOutputSize(path string, fallback int64) int64 {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	info, err := os.Stat(path)
	if err != nil {
		return fallback
	}
	return info.Size()
}

func normalizeJobNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now()
	}
	return now
}

func environmentJobLeaseID(id string) string {
	return environmentJobLeasePrefix + id
}

// EnvironmentJobIDFromLeaseID recovers the job a lease is held for, and reports
// false for a lease held by anything else. Callers that want to act on the job
// behind a lease need the inverse of the id scheme, and re-deriving the prefix
// at each call site is how the two drift apart.
func EnvironmentJobIDFromLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, environmentJobLeasePrefix)
	if id == leaseID || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}
