package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// The supervisor is the only writer of a job record: it waits on the work, so
// it alone observes the exit status first-hand, and it holds the job's lease.

// environmentJobLogPath holds the work's output, and the supervisor's own
// output when it dies before registering.
func environmentJobLogPath(dir, id string) string {
	return filepath.Join(dir, id+".log")
}

// jobRecorder is the single writer of the job record, so a progress tick can
// never overwrite the captured outcome.
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

// resolveSupervisorJobIdentity validates the supervisor's params; it never
// rebuilds the argv.
func resolveSupervisorJobIdentity(params model.EnvironmentJobSupervisorParams) (string, string, string, error) {
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

// registerEnvironmentJob writes the running record before any work starts.
func registerEnvironmentJob(params model.EnvironmentJobSupervisorParams) (*jobRecorder, error) {
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
		// Stamped with the running record so a running job is never seen without a beat.
		LastAliveAt:      now,
		LogPath:          environmentJobLogPath(dir, id),
		OutputLimitBytes: limit,
		LeaseID:          environmentJobLeaseID(id),
		Hostname:         build.CurrentJobHostname(),
		// The explicit override wins; otherwise ERUN_JOB_ID inherited from a parent job.
		StartedByJobID: core.FirstNonEmptyString(strings.TrimSpace(params.StartedByJobID), strings.TrimSpace(os.Getenv(shared.EnvironmentJobIDEnvVar))),
		Handoff:        params.Handoff,
		Exclusive:      params.Exclusive,
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return nil, err
	}
	return &jobRecorder{dir: dir, job: job}, nil
}

// RunEnvironmentJobSupervisor runs and records a job, returning once its outcome
// is durable. After registration every exit, including a panic, records an outcome.
func RunEnvironmentJobSupervisor(params model.EnvironmentJobSupervisorParams) error {
	// Must precede the work: the kernel decides reparenting when the spawner exits.
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
	// Deferred first so it runs after the body's own deferred stops.
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

// recordEnvironmentJobSupervisorFailure records why a supervisor ended without
// an outcome, so the record never reads "running" past its death. No-op once
// an outcome is durable.
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

func runRegisteredEnvironmentJobSupervisor(recorder *jobRecorder, params model.EnvironmentJobSupervisorParams, adoptedBaseline map[int]struct{}) error {
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
	// Shared with the writer so progress keeps moving after the log hits its cap.
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

	// The deadline is set once so the reinvocation budget covers the whole chain.
	command := params.Command
	reinvocationDeadline := time.Now().Add(resolveEnvironmentJobReinvocationBudget())
	for {
		cmd := core.Command(command[0], command[1:]...)
		cmd.Dir = params.Dir
		// ERUN_JOB_ID lets nested job starts record this job as their parent; it is
		// stable across reinvoked turns.
		cmd.Env = append(append(os.Environ(), sortedEnvironmentJobEnvPairs(params.Env)...), shared.EnvironmentJobIDEnvVar+"="+job.ID)
		cmd.Stdin = nil
		cmd.Stdout = writer
		cmd.Stderr = writer
		// Own process group so cancel reaches the whole tree but not the supervisor.
		core.DetachEnvironmentJobChild(cmd)

		var childPID int
		var procState *os.ProcessState
		var waitErr error
		if startErr := cmd.Start(); startErr != nil {
			waitErr = startErr
		} else {
			childPID = cmd.Process.Pid
			recorder.update(func(job *shared.EnvironmentJob) { job.ChildPID = childPID })
			// Reap only while the job's command is the sole os/exec child, so the reaper
			// cannot steal cmd.Wait's exit status.
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
