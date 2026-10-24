package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
)

const (
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

	environmentJobDirName = "jobs"

	// EnvironmentJobIDEnvVar is the env var a job's own supervisor sets on the
	// work's process, naming the job the work is running as. Anything the work
	// spawns inherits it the same way it inherits any other environment
	// variable, so a nested `job start` run from inside it (an agent detaching
	// a gate from its own Bash tool) reads it back and records
	// StartedByJobID without either side threading it through explicitly.
	EnvironmentJobIDEnvVar = "ERUN_JOB_ID"
)

// CurrentEnvironmentJobID returns this process's own job id -- the job whose
// supervisor set ERUN_JOB_ID on it -- or "" outside any job. registerEnvironmentJob
// reads this same env var directly for a job started as a plain nested
// subprocess (an agent's own Bash tool), where the inheritance
// happens for free. It is exported for a caller that cannot rely on that
// inheritance: forwarding a job-start request through the MCP edge crosses
// into that server's own long-lived process, which was never itself started as
// anyone's job and so has no ERUN_JOB_ID to inherit from, however deep the
// logical nesting on the calling side. Such a caller reads its own id here and
// threads it explicitly as StartEnvironmentJobParams.StartedByJobID.
func CurrentEnvironmentJobID() string {
	return strings.TrimSpace(os.Getenv(EnvironmentJobIDEnvVar))
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
	// EndedAt is zero until the job ends, so omitzero keeps a running job from
	// reading as one that ended at the zero instant.
	EndedAt time.Time `json:"endedAt,omitzero"`
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
	// CancelledByJobID names the job that asked this job to stop (via
	// CancelEnvironmentJob, read from that caller's own ERUN_JOB_ID at the
	// moment it signalled this job), empty when this job was never cancelled
	// or was cancelled from outside any job. It is what lets a parent whose
	// own finish check found this job among its failed children
	// (environmentJobFailedChildren) tell "I asked for this to stop" from
	// "this died on its own" — only the latter is a real failure of work the
	// parent waited on.
	CancelledByJobID string `json:"cancelledByJobId,omitempty"`
	// Exclusive says this job holds the environment to itself for its
	// lifetime: while it runs, every other job start in this environment is
	// refused and told this job holds it (see job_exclusive.go). It is what
	// this job actually holds, not what its caller asked for — a job running
	// under a claim its own ancestor already took reads as false here, because
	// it took no claim of its own and releasing one on its way out would drop
	// the ancestor's.
	Exclusive bool `json:"exclusive,omitempty"`

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
	LastAliveAt time.Time `json:"lastAliveAt,omitzero"`
	// AliveSeq is a monotonic counter bumped on every beat, so a caller can
	// tell "still beating" from "the same timestamp read twice".
	AliveSeq int64 `json:"aliveSeq,omitempty"`
	// AliveAgeMs is computed fresh on every read as now-LastAliveAt, in the
	// pod's own clock, and is nil only when the job has never beaten — an
	// attached job that has not renewed, which has no supervisor to beat for
	// it. Every record a supervisor wrote for itself carries a beat from the
	// instant it was published as running. A caller
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

func EnvironmentJobDir(tenant, environment string) (string, error) {
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentJobDirName), nil
}

func ReadEnvironmentJob(path string) (EnvironmentJob, error) {
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
