package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/build/jobhost"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// A task job runs Go work in this process, so MCP tools keep their typed
// results; this process is its supervisor. The work writes its own log, and
// StartedByJobID comes from the caller since nothing here inherits ERUN_JOB_ID.

// TaskEnvironmentJobParams is the work an in-process job runs.
type TaskEnvironmentJobParams struct {
	Tenant         string
	Environment    string
	Name           string
	ID             string
	LeaseTTL       time.Duration
	MaxOutputBytes int64
	// StartedByJobID is empty when no job started this work; never guessed.
	StartedByJobID string
	Handoff        bool
	// Run returns the result recorded on the job and writes what it would have printed to log.
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

// StartTaskEnvironmentJob runs work in a goroutine and returns once the record is durable.
func StartTaskEnvironmentJob(params TaskEnvironmentJobParams) (jobstate.EnvironmentJob, error) {
	params, err := params.normalize()
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	dir, err := jobexclusive.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(core.Context{}, dir, params.ID); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	logPath := filepath.Join(dir, params.ID+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}

	job := jobstate.EnvironmentJob{
		ID:               params.ID,
		Name:             params.Name,
		State:            EnvironmentJobStateRunning,
		Kind:             EnvironmentJobKindTask,
		PID:              os.Getpid(),
		StartedAt:        time.Now(),
		LeaseID:          environmentJobLeaseID(params.ID),
		Hostname:         jobhost.CurrentJobHostname(),
		StartedByJobID:   params.StartedByJobID,
		Handoff:          params.Handoff,
		LogPath:          logPath,
		OutputLimitBytes: params.MaxOutputBytes,
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		_ = log.Close()
		return jobstate.EnvironmentJob{}, err
	}
	recorder := &jobRecorder{dir: dir, job: job}
	writer := &jobOutputWriter{file: log, limit: params.MaxOutputBytes, onTruncate: func() {
		recorder.update(func(job *jobstate.EnvironmentJob) { job.OutputTruncated = true })
	}}

	// Never exclusive: no start call could have claimed the environment for it.
	beat, stopBeat := startEnvironmentJobHeartbeat(params.Tenant, params.Environment, recorder, params.LeaseTTL, nil, false)
	stopAlive := startEnvironmentJobAliveBeat(recorder)
	go runTaskEnvironmentJob(recorder, beat, stopBeat, stopAlive, log, writer, params.Run)

	return recorder.snapshot(), nil
}

// runTaskEnvironmentJob runs the work and is the sole writer of its outcome.
func runTaskEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, stopBeat, stopAlive func(), log *os.File, writer *jobOutputWriter, run func(io.Writer) (any, error)) {
	defer func() { _ = log.Close() }()

	result, err := runTaskEnvironmentJobBody(writer, run)
	beat.refresh(false)

	// Stop the beats before the outcome is written, not deferred, so no reader
	// sees "finished" while the lease release is still pending.
	stopAlive()
	stopBeat()

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

	recorder.update(func(job *jobstate.EnvironmentJob) {
		job.State = jobstate.EnvironmentJobStateExited
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

// runTaskEnvironmentJobBody recovers a panic into an error so the job still records an outcome.
func runTaskEnvironmentJobBody(log io.Writer, run func(io.Writer) (any, error)) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("task panicked: %v", r)
		}
	}()
	return run(log)
}
