package eruncommon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A task job runs Go work asynchronously in this process instead of a
// subprocess a supervisor waits on. It exists for the MCP tools that already
// call shared erun-common logic directly in-process (build, deploy, doctor,
// and the rest of the job-envelope surface) — reaching for the command/agent
// job's re-exec-and-wait shape for them would mean spawning a subprocess of
// this same binary just to make a Go function call, and reconstructing every
// tool's argv and JSON output shape a second time to get the typed result
// back. Running the closure directly keeps the exact result type the caller
// would have gotten synchronously; only when it observes the outcome changes.
//
// This process is the task's supervisor, so its own liveness is what a reader
// checks: as long as it is up, a task job that never records an outcome is a
// bug in the work (or a panic, recovered and turned into a failed outcome
// below), not a question of whether the supervisor is still there.
//
// Two things a task job records that its in-process shape does not give it for
// free, both of which a reader needs and neither of which the work itself can
// supply:
//
// A log. There is no subprocess whose stdio the supervisor could capture, so
// the work is handed the writer instead and mirrors into it whatever it would
// have printed. Without one, a failed task left a bare exit code and a bare
// error string behind — no argv, no output, nothing to diagnose from after the
// fact, which is exactly how a real build failure became indistinguishable
// from any other.
//
// A parent. Nothing in this process inherits ERUN_JOB_ID the way a nested
// subprocess does: this server was never started as anyone's job, however deep
// the logical nesting on the calling side. StartedByJobID is therefore supplied
// by the caller, and it is what lets the job that started this work find it
// again on its own finish path (see EnvironmentJobStateGateIncomplete) rather
// than reporting its own clean exit as the whole story.

// TaskEnvironmentJobParams is the work an in-process job runs.
type TaskEnvironmentJobParams struct {
	Tenant      string
	Environment string
	Name        string
	// ID defaults to the name, matching every other job kind.
	ID       string
	LeaseTTL time.Duration
	// MaxOutputBytes caps the log below, matching a command job's own cap.
	MaxOutputBytes int64
	// StartedByJobID names the job this work is being done on behalf of (see
	// EnvironmentJob.StartedByJobID). Empty when nothing started it, which is
	// the honest answer for a caller driving this environment from outside any
	// job at all — never a guess at whichever job happens to be running here.
	StartedByJobID string
	// Handoff marks work deliberately meant to outlive whatever started it, so
	// the parent named above does not wait for it (see EnvironmentJob.Handoff).
	Handoff bool
	// Run does the work and returns its result, JSON-marshalled verbatim onto
	// the job record once it finishes. It writes to log whatever the same call
	// would have printed had the caller run it synchronously.
	Run func(log io.Writer) (any, error)
}

func (p TaskEnvironmentJobParams) normalize() (TaskEnvironmentJobParams, error) {
	p.Tenant = strings.TrimSpace(p.Tenant)
	p.Environment = strings.TrimSpace(p.Environment)
	p.Name = strings.TrimSpace(p.Name)
	p.StartedByJobID = strings.TrimSpace(p.StartedByJobID)
	id, err := normalizeEnvironmentJobIdentity(p.Tenant, p.Environment, p.Name, p.ID)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.LeaseTTL, err = normalizeEnvironmentJobLeaseTTL(p.LeaseTTL); err != nil {
		return p, err
	}
	if p.MaxOutputBytes, err = normalizeEnvironmentJobOutputLimit(p.MaxOutputBytes); err != nil {
		return p, err
	}
	if p.Run == nil {
		return p, fmt.Errorf("task job requires work to run")
	}
	return p, nil
}

// StartTaskEnvironmentJob runs work in the background and returns its handle
// once the record is durable. Unlike StartEnvironmentJob there is nothing to
// detach or wait for here: the goroutine below is this call's whole
// supervisor, running for as long as this process does.
func StartTaskEnvironmentJob(params TaskEnvironmentJobParams) (EnvironmentJob, error) {
	params, err := params.normalize()
	if err != nil {
		return EnvironmentJob{}, err
	}
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(Context{}, dir, params.ID); err != nil {
		return EnvironmentJob{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EnvironmentJob{}, err
	}
	logPath := filepath.Join(dir, params.ID+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return EnvironmentJob{}, err
	}

	job := EnvironmentJob{
		ID:               params.ID,
		Name:             params.Name,
		State:            EnvironmentJobStateRunning,
		Kind:             EnvironmentJobKindTask,
		PID:              os.Getpid(),
		StartedAt:        time.Now(),
		LeaseID:          environmentJobLeaseID(params.ID),
		Hostname:         currentJobHostname(),
		StartedByJobID:   params.StartedByJobID,
		Handoff:          params.Handoff,
		LogPath:          logPath,
		OutputLimitBytes: params.MaxOutputBytes,
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		_ = log.Close()
		return EnvironmentJob{}, err
	}
	recorder := &jobRecorder{dir: dir, job: job}
	writer := &jobOutputWriter{file: log, limit: params.MaxOutputBytes, onTruncate: func() {
		recorder.update(func(job *EnvironmentJob) { job.OutputTruncated = true })
	}}

	// Never exclusive: a task job is Go work inside this server's own
	// long-lived process, not a detached supervisor, so it has no start call
	// that could have claimed the environment on its behalf.
	beat, stopBeat := startEnvironmentJobHeartbeat(params.Tenant, params.Environment, recorder, params.LeaseTTL, nil, false)
	stopAlive := startEnvironmentJobAliveBeat(recorder)
	go runTaskEnvironmentJob(recorder, beat, stopBeat, stopAlive, log, writer, params.Run)

	return recorder.snapshot(), nil
}

// runTaskEnvironmentJob is the task's whole life after it is registered: run
// the work, recover a panic into a failed outcome rather than leaving the
// record running forever, and record whatever it produced. It is the only
// writer of this job's outcome, matching every other kind.
func runTaskEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, stopBeat, stopAlive func(), log *os.File, writer *jobOutputWriter, run func(io.Writer) (any, error)) {
	defer func() { _ = log.Close() }()
	defer stopAlive()
	defer stopBeat()

	result, err := runTaskEnvironmentJobBody(writer, run)
	// Fold the lease's final renewal before the outcome lands, matching the
	// command/agent supervisor's own shutdown order.
	beat.refresh(false)

	code := 0
	reason := ""
	if err != nil {
		code = 1
		reason = err.Error()
	}
	var payload json.RawMessage
	if result != nil {
		encoded, marshalErr := json.Marshal(result)
		switch {
		case marshalErr != nil && reason == "":
			code = 1
			reason = fmt.Sprintf("task result could not be encoded as JSON: %v", marshalErr)
		case marshalErr == nil:
			payload = encoded
		}
	}

	recorder.update(func(job *EnvironmentJob) {
		job.State = EnvironmentJobStateExited
		job.EndedAt = time.Now()
		job.ExitCode = &code
		job.Reason = reason
		job.OutputBytes = writer.written()
		job.OutputTruncated = writer.truncated()
		if len(payload) > 0 {
			job.Result = payload
		}
	})
}

// runTaskEnvironmentJobBody recovers a panic into an error so a bug in the
// work still produces a recorded outcome instead of a job stuck running
// forever — this process staying alive would otherwise read as "still going".
func runTaskEnvironmentJobBody(log io.Writer, run func(io.Writer) (any, error)) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("task panicked: %v", r)
		}
	}()
	return run(log)
}
