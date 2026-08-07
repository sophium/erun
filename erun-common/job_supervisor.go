package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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

// jobSupervisorReportTimeout bounds how long a start waits for the supervisor to
// register the job before calling the start failed.
const jobSupervisorReportTimeout = 10 * time.Second

// StartEnvironmentJobParams is the work to detach.
type StartEnvironmentJobParams struct {
	Tenant      string
	Environment string
	Name        string
	// ID defaults to the name, matching the lease store, so re-running the same
	// named work keeps one stable handle.
	ID      string
	Command []string
	Dir     string
	// MaxOutputBytes caps the captured output; zero takes the default.
	MaxOutputBytes int64
	// LeaseTTL is how long the job's activity lease holds between renewals; the
	// supervisor renews well inside it for as long as the work runs.
	LeaseTTL time.Duration
	// SupervisorPath is the erun executable that will supervise the job. Each
	// transport resolves it — the CLI is already that executable, the MCP server
	// finds it on the environment's PATH — so this package stays free of any
	// assumption about which binary is calling.
	SupervisorPath string
}

func (p StartEnvironmentJobParams) normalize() (StartEnvironmentJobParams, error) {
	p.Tenant = strings.TrimSpace(p.Tenant)
	p.Environment = strings.TrimSpace(p.Environment)
	p.Name = strings.TrimSpace(p.Name)
	id, err := normalizeEnvironmentJobIdentity(p.Tenant, p.Environment, p.Name, p.ID)
	if err != nil {
		return p, err
	}
	p.ID = id
	if len(p.Command) == 0 || strings.TrimSpace(p.Command[0]) == "" {
		return p, fmt.Errorf("job command is required")
	}
	if strings.TrimSpace(p.SupervisorPath) == "" {
		return p, fmt.Errorf("job supervisor executable is required")
	}
	if p.MaxOutputBytes, err = normalizeEnvironmentJobOutputLimit(p.MaxOutputBytes); err != nil {
		return p, err
	}
	if p.LeaseTTL, err = normalizeEnvironmentJobLeaseTTL(p.LeaseTTL); err != nil {
		return p, err
	}
	return p, nil
}

// normalizeEnvironmentJobIdentity resolves the target and the handle a job is
// addressed by, shared by starting and attaching. The id defaults to the name,
// matching the lease store, so re-running the same named work keeps one handle.
func normalizeEnvironmentJobIdentity(tenant, environment, name, id string) (string, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return "", fmt.Errorf("tenant and environment are required")
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("job name is required")
	}
	value := strings.TrimSpace(id)
	if value == "" {
		value = name
	}
	return resolveEnvironmentJobID(value)
}

func normalizeEnvironmentJobOutputLimit(limit int64) (int64, error) {
	if limit == 0 {
		return DefaultEnvironmentJobOutputLimitBytes, nil
	}
	if limit < 0 {
		return 0, fmt.Errorf("max output bytes must not be negative")
	}
	return limit, nil
}

func normalizeEnvironmentJobLeaseTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		return DefaultEnvironmentActivityLeaseTTL, nil
	}
	if ttl < 0 {
		return 0, fmt.Errorf("lease ttl must be greater than zero")
	}
	return ttl, nil
}

// StartEnvironmentJob detaches the work and returns the handle to it.
func StartEnvironmentJob(ctx Context, params StartEnvironmentJobParams) (EnvironmentJob, error) {
	params, err := params.normalize()
	if err != nil {
		return EnvironmentJob{}, err
	}
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(ctx, dir, params); err != nil {
		return EnvironmentJob{}, err
	}

	logPath := filepath.Join(dir, params.ID+".log")
	args := environmentJobSupervisorArgs(params)
	ctx.Trace(fmt.Sprintf("job: detaching %q as job %s, output at %s", params.Name, params.ID, logPath))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s for the job's lifetime", environmentJobLeaseID(params.ID)))
	ctx.TraceCommand(params.Dir, params.SupervisorPath, args...)
	if ctx.DryRun {
		return plannedEnvironmentJob(params, logPath), nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EnvironmentJob{}, err
	}
	cmd := Command(params.SupervisorPath, args...)
	// The supervisor inherits nothing from the caller's terminal: no working
	// directory it could pin, no stdio it could block on, and its own session, so
	// the transport dropping or the caller exiting cannot take it with them.
	cmd.Dir = ""
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachEnvironmentJobSupervisor(cmd)
	if err := cmd.Start(); err != nil {
		return EnvironmentJob{}, fmt.Errorf("start job supervisor: %w", err)
	}
	// Reap the supervisor when it eventually exits. A long-lived caller (the MCP
	// server) would otherwise collect a zombie per job.
	go func() { _ = cmd.Wait() }()

	job, err := awaitEnvironmentJobRecord(dir, params.ID, cmd.Process.Pid)
	if err != nil {
		return EnvironmentJob{}, err
	}
	return job, nil
}

// reserveEnvironmentJobID makes the id unambiguous before anything is spawned.
// Reusing the id of work that is still running would leave two supervisors
// writing one record, so it is refused; reusing a finished one replaces it, and
// says so, because the alternative is an orchestrator unable to re-run named
// work without inventing ids.
func reserveEnvironmentJobID(ctx Context, dir string, params StartEnvironmentJobParams) error {
	existing, err := readEnvironmentJob(filepath.Join(dir, params.ID+".json"))
	if err != nil {
		return nil
	}
	resolved := reconcileEnvironmentJob(dir, existing, time.Now(), processAlive)
	if !resolved.Finished() {
		return fmt.Errorf("job %q is already running (pid %d); pass a different id or cancel it first", params.ID, resolved.PID)
	}
	ctx.Trace(fmt.Sprintf("job: replacing the finished job record %s from %s", params.ID, resolved.StartedAt.UTC().Format(time.RFC3339)))
	if ctx.DryRun {
		return nil
	}
	removeEnvironmentJobFiles(dir, params.ID)
	return nil
}

// awaitEnvironmentJobRecord waits for the supervisor to register the job, so a
// start either returns a handle that resolves or fails outright — never a handle
// to nothing.
func awaitEnvironmentJobRecord(dir, id string, supervisorPID int) (EnvironmentJob, error) {
	deadline := time.Now().Add(jobSupervisorReportTimeout)
	for {
		job, err := readEnvironmentJob(filepath.Join(dir, id+".json"))
		if err == nil {
			return job, nil
		}
		if !time.Now().Before(deadline) {
			return EnvironmentJob{}, fmt.Errorf("job supervisor %d did not register job %q within %s", supervisorPID, id, jobSupervisorReportTimeout)
		}
		if !processAlive(supervisorPID) {
			return EnvironmentJob{}, fmt.Errorf("job supervisor %d exited without registering job %q", supervisorPID, id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func plannedEnvironmentJob(params StartEnvironmentJobParams, logPath string) EnvironmentJob {
	return EnvironmentJob{
		ID:               params.ID,
		Name:             params.Name,
		State:            EnvironmentJobStateRunning,
		Command:          append([]string(nil), params.Command...),
		Dir:              params.Dir,
		LogPath:          logPath,
		OutputLimitBytes: params.MaxOutputBytes,
		LeaseID:          environmentJobLeaseID(params.ID),
	}
}

// environmentJobSupervisorArgs is the one place the two halves of a job agree on
// how the supervisor is invoked. It lives beside the supervisor body so a change
// to either is a change to both.
func environmentJobSupervisorArgs(params StartEnvironmentJobParams) []string {
	args := []string{
		"job", "supervise",
		"--tenant", params.Tenant,
		"--environment", params.Environment,
		"--id", params.ID,
		"--name", params.Name,
		"--max-output-bytes", strconv.FormatInt(params.MaxOutputBytes, 10),
		"--lease-ttl", params.LeaseTTL.String(),
	}
	if strings.TrimSpace(params.Dir) != "" {
		args = append(args, "--dir", params.Dir)
	}
	args = append(args, "--")
	return append(args, params.Command...)
}

// EnvironmentJobSupervisorParams is what the supervisor process is handed.
type EnvironmentJobSupervisorParams struct {
	Tenant         string
	Environment    string
	ID             string
	Name           string
	Dir            string
	Command        []string
	MaxOutputBytes int64
	LeaseTTL       time.Duration
}

// registerEnvironmentJob writes the running record before any work starts, so
// the handle exists from the moment the start call can observe it — including
// for work that then fails to exec.
func registerEnvironmentJob(params EnvironmentJobSupervisorParams) (string, EnvironmentJob, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = strings.TrimSpace(params.ID)
	}
	id, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, name, params.ID)
	if err != nil {
		return "", EnvironmentJob{}, err
	}
	if len(params.Command) == 0 || strings.TrimSpace(params.Command[0]) == "" {
		return "", EnvironmentJob{}, fmt.Errorf("job command is required")
	}
	limit, err := normalizeEnvironmentJobOutputLimit(params.MaxOutputBytes)
	if err != nil {
		return "", EnvironmentJob{}, err
	}
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return "", EnvironmentJob{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", EnvironmentJob{}, err
	}
	job := EnvironmentJob{
		ID:               id,
		Name:             name,
		State:            EnvironmentJobStateRunning,
		Command:          append([]string(nil), params.Command...),
		Dir:              params.Dir,
		PID:              os.Getpid(),
		StartedAt:        time.Now(),
		LogPath:          filepath.Join(dir, id+".log"),
		OutputLimitBytes: limit,
		LeaseID:          environmentJobLeaseID(id),
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return "", EnvironmentJob{}, err
	}
	return dir, job, nil
}

// RunEnvironmentJobSupervisor is the supervisor body: register the job, hold its
// lease, run the work with its output captured, and record the outcome it
// observed. It returns only after the work has finished and its result is
// durable, so the caller of this function is the process whose liveness the job
// record is reconciled against.
func RunEnvironmentJobSupervisor(params EnvironmentJobSupervisorParams) error {
	dir, job, err := registerEnvironmentJob(params)
	if err != nil {
		return err
	}
	log, err := os.OpenFile(job.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	ttl, err := normalizeEnvironmentJobLeaseTTL(params.LeaseTTL)
	if err != nil {
		return err
	}
	stop := holdEnvironmentJobLease(params.Tenant, params.Environment, job, ttl)
	defer stop()

	writer := &jobOutputWriter{file: log, limit: job.OutputLimitBytes, onTruncate: func() {
		truncated := job
		truncated.OutputTruncated = true
		_ = writeEnvironmentJob(dir, truncated)
	}}

	cmd := Command(params.Command[0], params.Command[1:]...)
	cmd.Dir = params.Dir
	cmd.Stdin = nil
	cmd.Stdout = writer
	cmd.Stderr = writer
	// The work runs in its own process group so a cancel can reach it and every
	// process it spawned without touching this supervisor — the bookkeeping has
	// to survive to report what happened.
	detachEnvironmentJobChild(cmd)

	if startErr := cmd.Start(); startErr != nil {
		return finishEnvironmentJob(dir, job, writer, nil, startErr)
	}
	job.ChildPID = cmd.Process.Pid
	_ = writeEnvironmentJob(dir, job)

	waitErr := cmd.Wait()
	return finishEnvironmentJob(dir, job, writer, cmd.ProcessState, waitErr)
}

// finishEnvironmentJob records the outcome the supervisor observed. This is the
// only place an exit status is ever produced, and it comes from waiting on the
// process — never from parsing output, and never from a shell's $?.
func finishEnvironmentJob(dir string, job EnvironmentJob, writer *jobOutputWriter, state *os.ProcessState, waitErr error) error {
	job.State = EnvironmentJobStateExited
	job.EndedAt = time.Now()
	job.OutputBytes = writer.written()
	job.OutputTruncated = writer.truncated()

	code := -1
	switch {
	case state != nil:
		code = state.ExitCode()
		if signal := environmentJobExitSignal(state); signal != "" {
			job.Signal = signal
			job.Reason = "terminated by signal " + signal
		}
	case waitErr != nil:
		job.Reason = "failed to start: " + waitErr.Error()
	}
	job.ExitCode = &code
	return writeEnvironmentJob(dir, job)
}

// holdEnvironmentJobLease takes the job's activity lease and renews it well
// inside its TTL for as long as the supervisor runs. Releasing on the way out is
// best-effort by design: if this process is killed instead, the lease records
// this pid, so the lease store reclaims it on the next read exactly as it would
// for any other abandoned holder.
func holdEnvironmentJobLease(tenant, environment string, job EnvironmentJob, ttl time.Duration) func() {
	take := func() {
		_, _ = TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        job.Name,
			ID:          job.LeaseID,
			PID:         job.PID,
			TTL:         ttl,
		})
	}
	take()

	interval := ttl / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				take()
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		_ = ReleaseEnvironmentActivityLease(tenant, environment, job.LeaseID)
	}
}

// jobOutputWriter captures the work's merged stdout and stderr up to the cap.
// Past the cap it stops writing and says so once, so a bounded log can never be
// mistaken for the whole story.
type jobOutputWriter struct {
	file       *os.File
	limit      int64
	onTruncate func()

	mu      sync.Mutex
	count   int64
	dropped bool
}

func (w *jobOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	room := w.limit - w.count
	if room <= 0 {
		first := !w.dropped
		w.dropped = true
		w.mu.Unlock()
		if first && w.onTruncate != nil {
			w.onTruncate()
		}
		return len(p), nil
	}
	chunk := p
	if int64(len(chunk)) > room {
		chunk = chunk[:room]
	}
	written, err := w.file.Write(chunk)
	w.count += int64(written)
	hitCap := w.count >= w.limit && !w.dropped
	if hitCap {
		w.dropped = true
	}
	w.mu.Unlock()
	if hitCap && w.onTruncate != nil {
		w.onTruncate()
	}
	if err != nil {
		return written, err
	}
	return len(p), nil
}

func (w *jobOutputWriter) written() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func (w *jobOutputWriter) truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}

// AttachEnvironmentJobParams registers work erun did not start.
type AttachEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Name        string
	// PID is the process to track. It is the only thing the attached job's state
	// is decided by, and erun never learns its exit status — nothing erun ran was
	// waiting on it — so it resolves to unknown rather than to a made-up outcome.
	PID int
	// LogPath is where the caller is already writing the work's output, so job
	// output can serve it without erun having captured it.
	LogPath  string
	LeaseTTL time.Duration
}

// AttachEnvironmentJob gives work started another way a handle and a lease.
func AttachEnvironmentJob(ctx Context, params AttachEnvironmentJobParams) (EnvironmentJob, error) {
	dir, job, ttl, err := resolveAttachedEnvironmentJob(params)
	if err != nil {
		return EnvironmentJob{}, err
	}
	ctx.Trace(fmt.Sprintf("job: attaching %q as job %s, tracking pid %d", job.Name, job.ID, job.PID))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s while pid %d lives", job.LeaseID, job.PID))
	if ctx.DryRun {
		return job, nil
	}
	if err := writeEnvironmentJob(dir, job); err != nil {
		return EnvironmentJob{}, err
	}
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant:      params.Tenant,
		Environment: params.Environment,
		Name:        job.Name,
		ID:          job.LeaseID,
		PID:         job.PID,
		TTL:         ttl,
	}); err != nil {
		return EnvironmentJob{}, err
	}
	return job, nil
}

func resolveAttachedEnvironmentJob(params AttachEnvironmentJobParams) (string, EnvironmentJob, time.Duration, error) {
	name := strings.TrimSpace(params.Name)
	id, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, name, params.ID)
	if err != nil {
		return "", EnvironmentJob{}, 0, err
	}
	if params.PID <= 0 {
		return "", EnvironmentJob{}, 0, fmt.Errorf("a pid to track is required; an attached job has nothing else to reconcile against")
	}
	ttl, err := normalizeEnvironmentJobLeaseTTL(params.LeaseTTL)
	if err != nil {
		return "", EnvironmentJob{}, 0, err
	}
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return "", EnvironmentJob{}, 0, err
	}
	job := EnvironmentJob{
		ID:        id,
		Name:      name,
		State:     EnvironmentJobStateRunning,
		PID:       params.PID,
		StartedAt: time.Now(),
		Attached:  true,
		LogPath:   strings.TrimSpace(params.LogPath),
		LeaseID:   environmentJobLeaseID(id),
	}
	// Re-attaching the same id renews rather than restarts, matching the lease
	// store, so a caller can refresh on a timer without tracking what it holds.
	if existing, err := readEnvironmentJob(filepath.Join(dir, id+".json")); err == nil && existing.Attached && existing.PID == params.PID {
		job.StartedAt = existing.StartedAt
	}
	job.OutputBytes = environmentJobOutputSize(job.LogPath, 0)
	return dir, job, ttl, nil
}

// CancelEnvironmentJobParams selects the job to signal.
type CancelEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	// Signal is TERM, INT, HUP, or KILL; empty means TERM.
	Signal string
}

// CancelEnvironmentJobResult reports what the cancel did.
type CancelEnvironmentJobResult struct {
	Job EnvironmentJob `json:"job"`
	// Signalled is false when the job had already finished, so a cancel that
	// raced the work's own exit is not reported as having stopped it.
	Signalled bool   `json:"signalled"`
	Signal    string `json:"signal,omitempty"`
	TargetPID int    `json:"targetPid,omitempty"`
}

// CancelEnvironmentJob signals the work behind a job. The target is the pid the
// record holds, never a command-line pattern — a pattern can match the caller's
// own shell, which is how a cancel once killed the sequence issuing it. The
// supervisor is deliberately not signalled, so it survives to record the
// outcome the cancel produced.
func CancelEnvironmentJob(ctx Context, params CancelEnvironmentJobParams) (CancelEnvironmentJobResult, error) {
	signal, err := normalizeEnvironmentJobSignal(params.Signal)
	if err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
	if err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result := CancelEnvironmentJobResult{Job: job, Signal: signal}
	if job.Finished() {
		ctx.Trace(fmt.Sprintf("job: %s already finished (%s), nothing to signal", job.ID, job.State))
		return result, nil
	}
	target := job.ChildPID
	if target <= 0 {
		target = job.PID
	}
	if target <= 0 {
		return CancelEnvironmentJobResult{}, fmt.Errorf("job %q records no process to signal", job.ID)
	}
	result.TargetPID = target
	ctx.Trace(fmt.Sprintf("job: sending SIG%s to process group %d (job %s)", signal, target, job.ID))
	if ctx.DryRun {
		return result, nil
	}
	if err := signalEnvironmentJobProcessGroup(target, signal); err != nil {
		return CancelEnvironmentJobResult{}, err
	}
	result.Signalled = true
	return result, nil
}

func normalizeEnvironmentJobSignal(signal string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(signal)), "SIG")))
	if value == "" {
		return "TERM", nil
	}
	switch value {
	case "TERM", "INT", "HUP", "KILL":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported signal %q: expected TERM, INT, HUP, or KILL", signal)
	}
}

// ResolveErunExecutable finds the erun binary a caller can hand to
// StartEnvironmentJobParams.SupervisorPath. A transport that is not itself the
// erun binary — the MCP server running in the environment — uses this, and gets
// a clear failure rather than a job that never registers.
func ResolveErunExecutable() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ERUN_ERUN_BIN")); override != "" {
		return override, nil
	}
	path, err := exec.LookPath("erun")
	if err != nil {
		return "", fmt.Errorf("the erun executable is required to supervise a job but was not found on PATH: %w", err)
	}
	return path, nil
}
