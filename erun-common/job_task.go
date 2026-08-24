package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
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

// TaskEnvironmentJobParams is the work an in-process job runs.
type TaskEnvironmentJobParams struct {
	Tenant      string
	Environment string
	Name        string
	// ID defaults to the name, matching every other job kind.
	ID       string
	LeaseTTL time.Duration
	// Run does the work and returns its result, JSON-marshalled verbatim onto
	// the job record once it finishes.
	Run func() (any, error)
}

func (p TaskEnvironmentJobParams) normalize() (TaskEnvironmentJobParams, error) {
	p.Tenant = strings.TrimSpace(p.Tenant)
	p.Environment = strings.TrimSpace(p.Environment)
	p.Name = strings.TrimSpace(p.Name)
	id, err := normalizeEnvironmentJobIdentity(p.Tenant, p.Environment, p.Name, p.ID)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.LeaseTTL, err = normalizeEnvironmentJobLeaseTTL(p.LeaseTTL); err != nil {
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

	job := EnvironmentJob{
		ID:        params.ID,
		Name:      params.Name,
		State:     EnvironmentJobStateRunning,
		Kind:      EnvironmentJobKindTask,
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		LeaseID:   environmentJobLeaseID(params.ID),
		Hostname:  currentJobHostname(),
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return EnvironmentJob{}, err
	}
	recorder := &jobRecorder{dir: dir, job: job}

	beat, stopBeat := startEnvironmentJobHeartbeat(params.Tenant, params.Environment, recorder, params.LeaseTTL, nil)
	stopAlive := startEnvironmentJobAliveBeat(recorder)
	go runTaskEnvironmentJob(recorder, beat, stopBeat, stopAlive, params.Run)

	return recorder.snapshot(), nil
}

// runTaskEnvironmentJob is the task's whole life after it is registered: run
// the work, recover a panic into a failed outcome rather than leaving the
// record running forever, and record whatever it produced. It is the only
// writer of this job's outcome, matching every other kind.
func runTaskEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, stopBeat, stopAlive func(), run func() (any, error)) {
	defer stopAlive()
	defer stopBeat()

	result, err := runTaskEnvironmentJobBody(run)
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
		if len(payload) > 0 {
			job.Result = payload
		}
	})
}

// runTaskEnvironmentJobBody recovers a panic into an error so a bug in the
// work still produces a recorded outcome instead of a job stuck running
// forever — this process staying alive would otherwise read as "still going".
func runTaskEnvironmentJobBody(run func() (any, error)) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("task panicked: %v", r)
		}
	}()
	return run()
}
