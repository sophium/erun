package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// DefaultEnvironmentJobOutputReadBytes bounds one output read so a poll
	// returns a page rather than the whole log.
	DefaultEnvironmentJobOutputReadBytes int64 = 64 << 10

	environmentJobDirName     = "jobs"
	environmentJobLeasePrefix = "job-"
)

// EnvironmentJob is one unit of long work and its outcome.
type EnvironmentJob struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is running, exited, or unknown. It is resolved on every read against
	// the supervisor's liveness, so a record can never claim to be running after
	// the process behind it is gone.
	State string `json:"state"`
	// Kind is command or agent. An agent job runs an AI tool in its streaming
	// mode, which is what lets it report progress rather than only a state.
	Kind string `json:"kind,omitempty"`
	// AgentTool names the AI tool an agent job runs, and is empty otherwise.
	AgentTool string `json:"agentTool,omitempty"`
	// Progress is the normalized view of an agent run, folded from the tool's
	// event stream by the supervisor. It is nil for a command job and for an
	// agent run that has not emitted yet — never a made-up zero state.
	Progress *AgentJobProgress `json:"progress,omitempty"`
	// Command is the argv the supervisor ran, empty for an attached job.
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
	// ExitCode is set for every job that reached exited, and is -1 when the work
	// was terminated by a signal. It is nil in every other state, so a missing
	// outcome can never be read as a zero one.
	ExitCode *int `json:"exitCode"`
	// Signal names the signal that ended the work, empty when it exited normally.
	Signal string `json:"signal,omitempty"`
	// Reason explains a non-obvious state: why the outcome is unknown, or how the
	// work ended when it was not a plain exit.
	Reason string `json:"reason,omitempty"`
	// Attached marks work erun did not start. Its outcome is never captured,
	// because nothing erun ran was in a position to observe it.
	Attached bool `json:"attached,omitempty"`

	LogPath          string `json:"logPath,omitempty"`
	OutputBytes      int64  `json:"outputBytes"`
	OutputLimitBytes int64  `json:"outputLimitBytes,omitempty"`
	// OutputTruncated says the cap was reached and later output was dropped, so a
	// short log is never mistaken for a quiet run.
	OutputTruncated bool `json:"outputTruncated,omitempty"`

	// LeaseID is the activity lease the job holds for its lifetime, which is why
	// starting one also makes the environment read as busy.
	LeaseID string `json:"leaseId,omitempty"`
}

// Finished reports whether the job reached a terminal state.
func (j EnvironmentJob) Finished() bool {
	return j.State == EnvironmentJobStateExited || j.State == EnvironmentJobStateUnknown
}

// Succeeded is the only definition of success: an outcome was captured and it
// was zero. An unknown job is never a success.
func (j EnvironmentJob) Succeeded() bool {
	return j.State == EnvironmentJobStateExited && j.ExitCode != nil && *j.ExitCode == 0
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
	return reconcileEnvironmentJob(dir, job, normalizeJobNow(now), processAlive), nil
}

// LoadEnvironmentJobs returns every retained job, newest first, pruning records
// that aged out as it reads.
func LoadEnvironmentJobs(tenant, environment string, now time.Time) ([]EnvironmentJob, error) {
	return loadEnvironmentJobs(tenant, environment, normalizeJobNow(now), processAlive)
}

func loadEnvironmentJobs(tenant, environment string, now time.Time, alive func(int) bool) ([]EnvironmentJob, error) {
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
		jobs = append(jobs, reconcileEnvironmentJob(dir, job, now, alive))
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
func reconcileEnvironmentJob(dir string, job EnvironmentJob, now time.Time, alive func(int) bool) EnvironmentJob {
	if job.State != EnvironmentJobStateRunning {
		return job
	}
	if job.PID > 0 && alive != nil && alive(job.PID) {
		job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
		return job
	}
	job.State = EnvironmentJobStateUnknown
	job.EndedAt = now
	job.ExitCode = nil
	if job.Attached {
		job.Reason = fmt.Sprintf("attached process %d is gone; erun did not run this work, so no exit status was recorded", job.PID)
	} else {
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; the runtime pod was most likely replaced", job.PID)
	}
	job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
	_ = writeEnvironmentJob(dir, job)
	return job
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
