package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Starting a job detaches it, and the detachment is erun's job rather than the
// caller's: a new session, output redirected to the job's log, and a supervisor
// that outlives the call that asked for it. That is the scaffolding every
// orchestrator used to hand-roll around a general-purpose escape hatch, and the
// place most of the observation bugs lived.
//
// The supervisor is the only writer of a job record. It is the process that
// waits on the work, so it is the only thing in a position to observe the exit
// status first-hand — no sentinel token in a log, no exit code re-expanded by an
// intermediate shell. It also holds the job's activity lease for the run, so a
// started job makes the environment read as busy without the caller arranging
// anything.

// environmentJobLogPath is the one place the capture path and every reader of it
// agree: the supervisor writes the work's output here, and a start reads back
// the supervisor's own last words from the same file when it never got that far.
func environmentJobLogPath(dir, id string) string {
	return filepath.Join(dir, id+".log")
}

// EnvironmentJobSupervisorParams is what the supervisor process is handed.
type EnvironmentJobSupervisorParams struct {
	Tenant      string
	Environment string
	ID          string
	Name        string
	Dir         string
	// Agent names the AI tool the command is a streaming invocation of, so the
	// supervisor folds its event stream into progress. Empty for a command job.
	Agent          string
	Command        []string
	Env            map[string]string
	MaxOutputBytes int64
	LeaseTTL       time.Duration
	// Handoff carries StartEnvironmentJobParams.Handoff into the record the
	// supervisor registers (see registerEnvironmentJob).
	Handoff bool
	// Exclusive says this job holds the environment's exclusivity claim, which
	// the start already took (see takeEnvironmentJobExclusivityClaim). The
	// supervisor renews it alongside its presence lease and drops it on the way
	// out; it never decides exclusivity itself, since by the time it runs the
	// question has already been answered and acted on.
	Exclusive bool
	// StartedByJobID carries StartEnvironmentJobParams.StartedByJobID's explicit
	// override into the record the supervisor registers; empty defers to this
	// process's own ERUN_JOB_ID (see registerEnvironmentJob).
	StartedByJobID string
}

// jobRecorder is the supervisor's single writer of the job record. The progress
// poll and the run's own milestones mutate one value under one lock, so a tick
// can never overwrite the outcome the wait just captured.
type jobRecorder struct {
	dir string
	mu  sync.Mutex
	job shared.EnvironmentJob
}

func (r *jobRecorder) update(mutate func(*shared.EnvironmentJob)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(&r.job)
	_ = writeEnvironmentJob(r.dir, r.job)
}

func (r *jobRecorder) snapshot() shared.EnvironmentJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.job
}

// resolveSupervisorJobIdentity settles what the supervisor is about to record.
// The supervisor is handed an already-built argv, so the agent tool is only the
// kind marker and the parser to fold its stream with — it never rebuilds the
// command, which keeps one source of truth for the argv at the start call.
func resolveSupervisorJobIdentity(params EnvironmentJobSupervisorParams) (string, string, string, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = strings.TrimSpace(params.ID)
	}
	id, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, name, params.ID)
	if err != nil {
		return "", "", "", err
	}
	if len(params.Command) == 0 || strings.TrimSpace(params.Command[0]) == "" {
		return "", "", "", fmt.Errorf("job command is required")
	}
	agent := strings.TrimSpace(params.Agent)
	if agent == "" {
		return id, name, "", nil
	}
	if agent, err = NormalizeAgentJobTool(agent); err != nil {
		return "", "", "", err
	}
	return id, name, agent, nil
}

// registerEnvironmentJob writes the running record before any work starts, so
// the handle exists from the moment the start call can observe it — including
// for work that then fails to exec.
func registerEnvironmentJob(params EnvironmentJobSupervisorParams) (*jobRecorder, error) {
	id, name, agent, err := resolveSupervisorJobIdentity(params)
	if err != nil {
		return nil, err
	}
	limit, err := normalizeEnvironmentJobOutputLimit(params.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	dir, err := shared.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	job := shared.EnvironmentJob{
		ID:        id,
		Name:      name,
		State:     EnvironmentJobStateRunning,
		Kind:      environmentJobKind(agent),
		AgentTool: agent,
		Command:   append([]string(nil), params.Command...),
		Dir:       params.Dir,
		PID:       os.Getpid(),
		StartedAt: now,
		// Stamped in the same write that publishes the job as running, so a
		// reader can never observe a running job that has not beaten: writing
		// this record *is* the supervisor being alive, and the first
		// startEnvironmentJobAliveBeat tick is only the next one. Leaving it
		// for that tick left an observable window -- registering, then opening
		// the log and taking the lease -- in which a running job reported no
		// heartbeat at all. An attached job has no supervisor and is stamped by
		// its own renew instead.
		LastAliveAt:      now,
		LogPath:          environmentJobLogPath(dir, id),
		OutputLimitBytes: limit,
		LeaseID:          environmentJobLeaseID(id),
		Hostname:         build.CurrentJobHostname(),
		// An explicit override (see StartEnvironmentJobParams.StartedByJobID)
		// wins when the start call could not rely on this process inheriting
		// ERUN_JOB_ID for free; otherwise this supervisor's own process
		// inherited it from whatever job started it (see the env this job's
		// own child process is given below), so a value here means this job
		// was started from inside another job's work rather than from
		// outside any job.
		StartedByJobID: core.FirstNonEmptyString(strings.TrimSpace(params.StartedByJobID), strings.TrimSpace(os.Getenv(shared.EnvironmentJobIDEnvVar))),
		Handoff:        params.Handoff,
		Exclusive:      params.Exclusive,
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return nil, err
	}
	return &jobRecorder{dir: dir, job: job}, nil
}

// RunEnvironmentJobSupervisor is the supervisor body: register the job, hold its
// lease, run the work with its output captured, and record the outcome it
// observed. It returns only after the work has finished and its result is
// durable, so the caller of this function is the process whose liveness the job
// record is reconciled against.
//
// Registering the job is the point of no return: from there on, this process is
// the only writer of that record's outcome, so every way its own body can end --
// a returned setup error, or a panic in its finish path -- is turned into a
// recorded outcome rather than into a record left reading "running". See
// recordEnvironmentJobSupervisorFailure.
func RunEnvironmentJobSupervisor(params EnvironmentJobSupervisorParams) error {
	// Claim orphaned descendants before any is created. This has to happen
	// before the work starts, not at the finish check that reads it: the
	// reparenting it arranges is decided by the kernel at the moment the
	// spawning process exits, and cannot be retrofitted afterwards.
	core.EnableEnvironmentJobSubreaper()
	adoptedBaseline := core.EnvironmentJobDescendantBaseline(os.Getpid())
	env, err := normalizeEnvironmentJobEnv(params.Env)
	if err != nil {
		return err
	}
	params.Env = env
	recorder, err := registerEnvironmentJob(params)
	if err != nil {
		return err
	}
	// Registered before runRegisteredEnvironmentJobSupervisor's own deferred
	// stops, so those have already run by the time this writes: a reader that
	// observes the terminal record never races this supervisor's own tail, the
	// same ordering job_task.go's runTaskEnvironmentJob takes for the same
	// reason.
	var panicked any
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
		recordEnvironmentJobSupervisorFailure(recorder, err, panicked)
	}()
	err = runRegisteredEnvironmentJobSupervisor(recorder, params, adoptedBaseline)
	return err
}

// recordEnvironmentJobSupervisorFailure settles a job whose supervising process
// is ending without an outcome of its own. A registered record still reading
// EnvironmentJobStateRunning is the worst shape this store can hold: once the
// supervisor's process is gone, every later read can only reconcile the record
// against that death and demote it to EnvironmentJobStateUnknown -- no exit
// code, and nothing naming what happened to the work. The supervisor itself did
// observe why it is ending, so recording that turns an unknowable fate into a
// definite, actionable one.
//
// A no-op once an outcome is already durable, so it can never overwrite the one
// finishEnvironmentJob wrote.
func recordEnvironmentJobSupervisorFailure(recorder *jobRecorder, err error, panicked any) {
	if recorder == nil || (err == nil && panicked == nil) {
		return
	}
	if recorder.snapshot().State != EnvironmentJobStateRunning {
		return
	}
	reason := fmt.Sprintf("job supervisor could not run this job: %v", err)
	if panicked != nil {
		reason = fmt.Sprintf("job supervisor panicked before it could record an outcome: %v", panicked)
	}
	code := 1
	recorder.update(func(job *shared.EnvironmentJob) {
		job.State = shared.EnvironmentJobStateExited
		job.EndedAt = time.Now()
		job.ExitCode = &code
		job.Reason = reason
	})
}

// runRegisteredEnvironmentJobSupervisor is RunEnvironmentJobSupervisor's body
// for a job whose running record is already durable.
func runRegisteredEnvironmentJobSupervisor(recorder *jobRecorder, params EnvironmentJobSupervisorParams, adoptedBaseline map[int]struct{}) error {
	job := recorder.snapshot()
	log, err := os.OpenFile(job.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	ttl, err := normalizeEnvironmentJobLeaseTTL(params.LeaseTTL)
	if err != nil {
		return err
	}
	// The agent reader is shared between the writer that feeds it every byte the
	// child process writes and the heartbeat that reads its current snapshot —
	// that sharing is what keeps progress moving after the log hits its cap.
	var agent *agentProgressReader
	if job.Kind == EnvironmentJobKindAgent {
		agent = newAgentProgressReader(job.AgentTool)
	}
	beat, stop := startEnvironmentJobHeartbeat(params.Tenant, params.Environment, recorder, ttl, agent, params.Exclusive)
	defer stop()
	stopAlive := startEnvironmentJobAliveBeat(recorder)
	defer stopAlive()

	writer := &jobOutputWriter{file: log, limit: job.OutputLimitBytes, agent: agent, onTruncate: func() {
		recorder.update(func(job *shared.EnvironmentJob) { job.OutputTruncated = true })
	}}

	// command is reassigned on a bounded reinvocation (see
	// considerEnvironmentJobReinvocation): a resumed turn replaces it with the
	// same tool invoked via AgentJobResumeCommand, run through the identical
	// loop body below. reinvocationDeadline is struck once, before the first
	// turn, so the wall-clock bound covers the whole chain rather than
	// resetting on each turn.
	command := params.Command
	reinvocationDeadline := time.Now().Add(resolveEnvironmentJobReinvocationBudget())
	for {
		cmd := core.Command(command[0], command[1:]...)
		cmd.Dir = params.Dir
		// ERUN_JOB_ID always rides along, not only when the caller sets Env: it is
		// what lets a nested `job start` run from inside this work (an agent
		// detaching a gate and awaiting it) record this job as its
		// StartedByJobID, so this job's own finish check can tell that nested job
		// apart from an unrelated one sharing the environment. It stays the same
		// job.ID across every reinvoked turn, which is what lets a job this turn
		// starts still be recognized as this job's own child.
		cmd.Env = append(append(os.Environ(), sortedEnvironmentJobEnvPairs(params.Env)...), shared.EnvironmentJobIDEnvVar+"="+job.ID)
		cmd.Stdin = nil
		cmd.Stdout = writer
		cmd.Stderr = writer
		// The work runs in its own process group so a cancel can reach it and every
		// process it spawned without touching this supervisor — the bookkeeping has
		// to survive to report what happened.
		core.DetachEnvironmentJobChild(cmd)

		var childPID int
		var procState *os.ProcessState
		var waitErr error
		if startErr := cmd.Start(); startErr != nil {
			waitErr = startErr
		} else {
			childPID = cmd.Process.Pid
			recorder.update(func(job *shared.EnvironmentJob) { job.ChildPID = childPID })
			// Reap the descendants this child orphans onto the supervisor while
			// it runs (see startEnvironmentJobChildReaper). Scoped to exactly
			// this window, in which the job's own command is the only os/exec
			// child this process has: cmd.Wait below is the one thing that can
			// report that child's exit status, and the supervisor's own helper
			// commands -- git, ps -- run outside it, under their own Waits.
			stopReaper := core.StartEnvironmentJobChildReaper(childPID)
			waitErr = cmd.Wait()
			stopReaper()
			procState = cmd.ProcessState
		}

		resumeCommand, reinvoke := considerEnvironmentJobReinvocation(recorder, beat, childPID, procState, waitErr, reinvocationDeadline, adoptedBaseline)
		if !reinvoke {
			return finishEnvironmentJob(recorder, beat, writer, childPID, procState, waitErr, adoptedBaseline)
		}
		command = resumeCommand
	}
}
