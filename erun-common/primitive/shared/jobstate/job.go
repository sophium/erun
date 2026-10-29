package jobstate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	EnvironmentJobStateExited = "exited"
	// EnvironmentJobStateUnknown: the record outlived its supervisor or pod.
	EnvironmentJobStateUnknown = "unknown"
	// EnvironmentJobStateAbandoned: the job exited leaving processes running in
	// its group; never success, whatever the exit code.
	EnvironmentJobStateAbandoned = "abandoned"
	// EnvironmentJobStateGateIncomplete: the job exited while a child job it
	// started was still running; never success, whatever the exit code.
	EnvironmentJobStateGateIncomplete = "gate-incomplete"

	EnvironmentJobDirName = "jobs"

	// EnvironmentJobIDEnvVar is inherited by everything the work spawns, so a
	// nested `job start` records its parent.
	EnvironmentJobIDEnvVar = "ERUN_JOB_ID"
)

// CurrentEnvironmentJobID is for callers, such as the MCP edge, whose server
// process never inherited ERUN_JOB_ID and must pass it explicitly.
func CurrentEnvironmentJobID() string {
	return strings.TrimSpace(os.Getenv(EnvironmentJobIDEnvVar))
}

// EnvironmentJob is one unit of long work and its outcome.
type EnvironmentJob struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is resolved against supervisor liveness on every read.
	State string `json:"state"`
	// Succeeded is computed on every read and is the only reliable success
	// check: ExitCode can be 0 while State or WorktreeDirty say otherwise.
	Succeeded bool   `json:"succeeded"`
	Kind      string `json:"kind,omitempty"`
	AgentTool string `json:"agentTool,omitempty"`
	// Progress is nil until an agent run emits, never a made-up zero state.
	Progress *AgentJobProgress `json:"progress,omitempty"`
	// Command is empty for attached and task jobs, which run no subprocess.
	Command []string `json:"command,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	// PID alone decides liveness: the supervisor, or the caller when attached.
	PID int `json:"pid,omitempty"`
	// ChildPID is what cancel signals, sparing the supervisor's bookkeeping.
	ChildPID  int       `json:"childPid,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
	// ExitCode is -1 for a signal and nil when no outcome was captured.
	ExitCode *int   `json:"exitCode"`
	Signal   string `json:"signal,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// UnknownReasonKind is Reason's machine-readable form when State is unknown.
	UnknownReasonKind string `json:"unknownReasonKind,omitempty"`
	// Attached marks work erun did not start; its outcome is never captured.
	Attached bool `json:"attached,omitempty"`
	// Hostname is the pod name, so a different one later proves pod replacement.
	Hostname string `json:"hostname,omitempty"`
	// StartedByJobID is the parent job; absent stays absent, never guessed.
	StartedByJobID string `json:"startedByJobId,omitempty"`
	// Handoff excludes this job from its parent's finish check, for work meant
	// to outlive the parent's turn.
	Handoff bool `json:"handoff,omitempty"`
	// CancelledByJobID lets a parent tell a child it stopped from one that failed.
	CancelledByJobID string `json:"cancelledByJobId,omitempty"`
	// Exclusive is false under an ancestor's claim, so exiting never drops it.
	Exclusive bool `json:"exclusive,omitempty"`

	LogPath          string `json:"logPath,omitempty"`
	OutputBytes      int64  `json:"outputBytes"`
	OutputLimitBytes int64  `json:"outputLimitBytes,omitempty"`
	OutputTruncated  bool   `json:"outputTruncated,omitempty"`

	LeaseID string `json:"leaseId,omitempty"`

	// LastAliveAt is pod-clock time; callers use AliveAgeMs, never their own clock.
	LastAliveAt time.Time `json:"lastAliveAt,omitzero"`
	AliveSeq    int64     `json:"aliveSeq,omitempty"`
	// AliveAgeMs past EnvironmentJobAliveStaleMs means an unknown outcome; nil
	// only for an attached job that never renewed.
	AliveAgeMs *int64 `json:"aliveAgeMs,omitempty"`

	// Result is a task job's return value as JSON, never flattened into a log.
	Result json.RawMessage `json:"result,omitempty"`

	// WorktreeDirty: a finished agent job left uncommitted work in its git tree.
	WorktreeDirty  bool   `json:"worktreeDirty,omitempty"`
	WorktreeBranch string `json:"worktreeBranch,omitempty"`
	// WorktreeDetached prevents a checkpoint commit, which would be unreachable.
	WorktreeDetached bool   `json:"worktreeDetached,omitempty"`
	WorktreeCommit   string `json:"worktreeCommit,omitempty"`
	// WorktreePushed: an unpushed checkpoint is as exposed to a lost pod as the
	// dirty tree it saved.
	WorktreePushed bool   `json:"worktreePushed,omitempty"`
	WorktreeRemote string `json:"worktreeRemote,omitempty"`
	WorktreeReason string `json:"worktreeReason,omitempty"`

	// CloneReclaimed: Dir was removed because it was clean and fully pushed.
	CloneReclaimed  bool   `json:"cloneReclaimed,omitempty"`
	CloneKeptReason string `json:"cloneKeptReason,omitempty"`

	// StartedJobFailed keeps a failed child visible behind this job's clean exit.
	StartedJobFailed string `json:"startedJobFailed,omitempty"`

	// ReinvocationCount at EnvironmentJobMaxReinvocations means the automatic
	// follow-ups are exhausted and someone must act.
	ReinvocationCount int `json:"reinvocationCount,omitempty"`
}

func (j EnvironmentJob) Finished() bool {
	return j.State == EnvironmentJobStateExited || j.State == EnvironmentJobStateUnknown ||
		j.State == EnvironmentJobStateAbandoned || j.State == EnvironmentJobStateGateIncomplete
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
