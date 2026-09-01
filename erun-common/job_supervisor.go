package eruncommon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
	// Agent names the AI tool to run as an agent job. Setting it replaces
	// Command: erun builds the tool's streaming invocation from Prompt, which is
	// the whole reason the kind exists — the tool's default mode reports nothing
	// until it exits.
	Agent  string
	Prompt string
	Dir    string
	// Env is additional environment for the job's own process, on top of what it
	// inherits from the environment's runtime pod (e.g. raising
	// CLAUDE_CODE_MAX_OUTPUT_TOKENS for one agent run). Values land in the
	// supervisor's argv, which anything able to list processes in this
	// environment can read — this is not where secrets belong. See
	// normalizeEnvironmentJobEnv for the names it refuses to set.
	Env map[string]string
	// MaxOutputBytes caps the captured output; zero takes the default.
	MaxOutputBytes int64
	// LeaseTTL is how long the job's activity lease holds between renewals; the
	// supervisor renews well inside it for as long as the work runs.
	LeaseTTL time.Duration
	// Handoff marks this job as deliberately meant to outlive whatever starts
	// it, excluding it from that caller's own finish check entirely (see
	// EnvironmentJob.Handoff). Only meaningful when this start itself runs
	// from inside another job's own work.
	Handoff bool
	// StartedByJobID is an explicit override for the job this new work should
	// record as its own parent (see EnvironmentJob.StartedByJobID). Leave it
	// empty for the common case: the supervisor this spawns inherits its own
	// process's ERUN_JOB_ID for free when the start itself runs as a plain
	// nested subprocess of another job's work (agent-gate.sh, an agent's own
	// Bash tool). Set it explicitly only when forwarding this start through a
	// channel that cannot carry that inheritance itself — the MCP edge, most
	// notably, since a request reaching it crosses into that server's own
	// long-lived process, which has no ERUN_JOB_ID of its own regardless of
	// how deep the logical nesting is on the calling side. See
	// CurrentEnvironmentJobID.
	StartedByJobID string
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
	p.StartedByJobID = strings.TrimSpace(p.StartedByJobID)
	id, err := normalizeEnvironmentJobIdentity(p.Tenant, p.Environment, p.Name, p.ID)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.Agent, p.Command, err = resolveEnvironmentJobWork(p.Agent, p.Prompt, p.Command); err != nil {
		return p, err
	}
	if p.Env, err = normalizeEnvironmentJobEnv(p.Env); err != nil {
		return p, err
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
	if p.Dir, err = resolveEnvironmentJobDir(p.Dir); err != nil {
		return p, err
	}
	return p, nil
}

// resolveEnvironmentJobDir resolves the directory the work runs in, and proves
// it usable before anything is detached.
//
// The supervisor is started with no inherited working directory, so a relative
// dir cannot mean "relative to the supervisor" — left alone it resolves against
// whatever directory the transport happens to be sitting in, which is not the
// repository. Anchor it to the project root instead, the same reading every
// other repo-facing surface gives a path, so a caller can name a subdirectory
// here exactly as they would to `raw`.
//
// The check happens now rather than at exec because Go reports an unusable
// cmd.Dir as an ENOENT naming the *binary*: an unverified dir surfaces as a
// missing shell, sending the reader after a broken image instead of a bad path.
func resolveEnvironmentJobDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	resolved := dir
	if !filepath.IsAbs(resolved) {
		if _, root, err := FindProjectRoot(); err == nil {
			resolved = filepath.Join(root, resolved)
		} else if abs, absErr := filepath.Abs(resolved); absErr == nil {
			resolved = abs
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("job dir %q does not exist (resolved to %s)", dir, resolved)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("job dir %q is not a directory (resolved to %s)", dir, resolved)
	}
	return resolved, nil
}

// normalizeEnvironmentJobIdentity resolves the target and the handle a job is
// addressed by, shared by starting and attaching. The id defaults to the name,
// matching the lease store, so re-running the same named work keeps one handle.
func normalizeEnvironmentJobIdentity(tenant, environment, name, id string) (string, error) {
	if err := errMissingTenantOrEnvironment("resolve environment job identity", tenant, environment); err != nil {
		return "", err
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

// resolveEnvironmentJobWork settles what the job runs. An agent job names a tool
// and a prompt and erun builds the argv; a command job brings its own. Naming
// both is refused rather than resolved, because a caller that meant one of them
// would otherwise silently get the other.
func resolveEnvironmentJobWork(agent, prompt string, command []string) (string, []string, error) {
	if strings.TrimSpace(agent) == "" {
		if strings.TrimSpace(prompt) != "" {
			return "", nil, fmt.Errorf("a prompt only applies to an agent job; name the agent tool or pass a command")
		}
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return "", nil, fmt.Errorf("job command is required")
		}
		return "", command, nil
	}
	if len(command) > 0 {
		return "", nil, fmt.Errorf("an agent job runs the tool's own streaming invocation; pass a prompt, not a command")
	}
	tool, err := NormalizeAgentJobTool(agent)
	if err != nil {
		return "", nil, err
	}
	built, err := AgentJobCommand(tool, prompt)
	if err != nil {
		return "", nil, err
	}
	return tool, built, nil
}

// jobEnvKeyPattern is the shape a caller-supplied job env var name must take:
// a plain identifier, the only thing that is ever a real environment variable
// name on the platforms erun runs on.
var jobEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jobEnvDeniedKeys are names a caller-supplied job env must never override.
// PATH/LD_PRELOAD/LD_LIBRARY_PATH/DYLD_* can redirect what the job's own
// process executes or dynamically loads — a code-execution hijack, not a
// tuning knob — and HOME/SHELL/IFS can subvert whatever script the job's
// command runs.
var jobEnvDeniedKeys = map[string]struct{}{
	"PATH": {}, "LD_PRELOAD": {}, "LD_LIBRARY_PATH": {},
	"DYLD_INSERT_LIBRARIES": {}, "DYLD_LIBRARY_PATH": {},
	"HOME": {}, "SHELL": {}, "IFS": {},
}

// normalizeEnvironmentJobEnv validates a caller-supplied job environment
// override. An `ERUN_` name is refused outright: that prefix is erun's own
// seam for redirecting its subprocess lookups (`ERUN_<NAME>_BIN`) and its
// detection overrides, and a job that later shells out to erun itself must not
// be able to redirect those from the outside.
func normalizeEnvironmentJobEnv(env map[string]string) (map[string]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if !jobEnvKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("job env var name %q is not a valid environment variable name", key)
		}
		upper := strings.ToUpper(key)
		if _, denied := jobEnvDeniedKeys[upper]; denied || strings.HasPrefix(upper, "ERUN_") {
			return nil, fmt.Errorf("job env var %q may not be set: it can redirect what the job's process executes or loads", key)
		}
		out[key] = value
	}
	return out, nil
}

// sortedEnvironmentJobEnvPairs renders a job env override as "KEY=VALUE"
// pairs in a stable order, so the supervisor argv and its dry-run trace never
// reorder between runs of the same request.
func sortedEnvironmentJobEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

// ensureAgentJobToolAvailable refuses an agent job before anything is spent —
// no activity lease, no log file, no supervisor process — when the named tool
// is not installed in this environment. That precondition is knowable up
// front; starting anyway trades a direct, actionable refusal for the tool's
// own failure a minute later, which for an unconfigured environment reads as
// a credential problem with no credential fix.
func ensureAgentJobToolAvailable(ctx Context, tool string) error {
	if strings.TrimSpace(tool) == "" {
		return nil
	}
	ctx.Trace(fmt.Sprintf("job: resolving agent tool %q in this environment", tool))
	// cmd.Err carries Go's own exec.LookPath phrasing, which is an
	// implementation detail rather than something to hand to the caller
	// verbatim; the tool name and "not available" already say what happened.
	if cmd := Command(tool); cmd.Err != nil {
		err := fmt.Errorf("agent tool %q is not available in this environment; install and configure it here, or start the job with a different --agent", tool)
		ctx.Trace(fmt.Sprintf("job: resolving agent tool %q failed: %v", tool, err))
		return err
	}
	return nil
}

// agentAuthFailureSignatures are phrases the AI tools use for their own auth
// failures. Matching one against a failed agent job's folded progress is what
// turns "the tool's raw message" into "this environment's credentials for
// that tool are missing or stale" — the actual problem, since there is often
// no in-band way to refresh them, unlike what the raw message suggests.
var agentAuthFailureSignatures = []string{
	"oauth session expired",
	"failed to authenticate",
	"authentication_error",
	"invalid api key",
	"please run /login",
	"not logged in",
}

// agentAuthFailureReason returns a clarified failure reason when a failed
// agent job's own folded error looks like an authentication problem, and ""
// otherwise — never guessing on a message that does not match one of the
// tools' own known phrasings.
func agentAuthFailureReason(tool, message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, signature := range agentAuthFailureSignatures {
		if strings.Contains(lower, signature) {
			return fmt.Sprintf("%s reported an authentication failure (%s); this environment's %s credentials are missing or stale, not a problem with the work itself", tool, trimmed, tool)
		}
	}
	return ""
}

// environmentJobKind names the kind a recorded tool implies, so the record and
// every reader agree without either re-deriving it.
func environmentJobKind(agentTool string) string {
	if strings.TrimSpace(agentTool) == "" {
		return EnvironmentJobKindCommand
	}
	return EnvironmentJobKindAgent
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
	if err := ensureAgentJobToolAvailable(ctx, params.Agent); err != nil {
		return EnvironmentJob{}, err
	}
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(ctx, dir, params.ID); err != nil {
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
func reserveEnvironmentJobID(ctx Context, dir, id string) error {
	existing, err := readEnvironmentJob(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil
	}
	resolved := reconcileEnvironmentJob(dir, existing, time.Now(), processAlive, currentJobHostname())
	if !resolved.Finished() {
		return fmt.Errorf("job %q is already running (pid %d); pass a different id or cancel it first", id, resolved.PID)
	}
	ctx.Trace(fmt.Sprintf("job: replacing the finished job record %s from %s", id, resolved.StartedAt.UTC().Format(time.RFC3339)))
	if ctx.DryRun {
		return nil
	}
	removeEnvironmentJobFiles(dir, id)
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
		Kind:             environmentJobKind(params.Agent),
		AgentTool:        params.Agent,
		Command:          append([]string(nil), params.Command...),
		Dir:              params.Dir,
		LogPath:          logPath,
		OutputLimitBytes: params.MaxOutputBytes,
		LeaseID:          environmentJobLeaseID(params.ID),
		Handoff:          params.Handoff,
	}
}

// environmentJobSupervisorArgs is the one place the two halves of a job agree on
// how the supervisor is invoked. It lives beside the supervisor body so a change
// to either is a change to both. The path is `exec job supervise` (#1246,
// following #1186's already-decided `job` -> `exec job` move) regardless of
// which entry point started the job -- `erun job start` and its deprecated
// top-level alias both resolve to the same canonical re-exec path.
func environmentJobSupervisorArgs(params StartEnvironmentJobParams) []string {
	args := []string{
		"exec", "job", "supervise",
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
	if strings.TrimSpace(params.Agent) != "" {
		args = append(args, "--agent", params.Agent)
	}
	if params.Handoff {
		args = append(args, "--handoff")
	}
	if params.StartedByJobID != "" {
		args = append(args, "--started-by-job-id", params.StartedByJobID)
	}
	for _, pair := range sortedEnvironmentJobEnvPairs(params.Env) {
		args = append(args, "--env", pair)
	}
	args = append(args, "--")
	return append(args, params.Command...)
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
	job EnvironmentJob
}

func (r *jobRecorder) update(mutate func(*EnvironmentJob)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(&r.job)
	_ = writeEnvironmentJob(r.dir, r.job)
}

func (r *jobRecorder) snapshot() EnvironmentJob {
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

// firstNonEmptyString returns the first non-empty value, or "" if all are.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
	dir, err := environmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	job := EnvironmentJob{
		ID:               id,
		Name:             name,
		State:            EnvironmentJobStateRunning,
		Kind:             environmentJobKind(agent),
		AgentTool:        agent,
		Command:          append([]string(nil), params.Command...),
		Dir:              params.Dir,
		PID:              os.Getpid(),
		StartedAt:        time.Now(),
		LogPath:          filepath.Join(dir, id+".log"),
		OutputLimitBytes: limit,
		LeaseID:          environmentJobLeaseID(id),
		Hostname:         currentJobHostname(),
		// An explicit override (see StartEnvironmentJobParams.StartedByJobID)
		// wins when the start call could not rely on this process inheriting
		// ERUN_JOB_ID for free; otherwise this supervisor's own process
		// inherited it from whatever job started it (see the env this job's
		// own child process is given below), so a value here means this job
		// was started from inside another job's work rather than from
		// outside any job.
		StartedByJobID: firstNonEmptyString(strings.TrimSpace(params.StartedByJobID), strings.TrimSpace(os.Getenv(environmentJobIDEnvVar))),
		Handoff:        params.Handoff,
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
func RunEnvironmentJobSupervisor(params EnvironmentJobSupervisorParams) error {
	env, err := normalizeEnvironmentJobEnv(params.Env)
	if err != nil {
		return err
	}
	params.Env = env
	recorder, err := registerEnvironmentJob(params)
	if err != nil {
		return err
	}
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
	beat, stop := startEnvironmentJobHeartbeat(params.Tenant, params.Environment, recorder, ttl, agent)
	defer stop()
	stopAlive := startEnvironmentJobAliveBeat(recorder)
	defer stopAlive()

	writer := &jobOutputWriter{file: log, limit: job.OutputLimitBytes, agent: agent, onTruncate: func() {
		recorder.update(func(job *EnvironmentJob) { job.OutputTruncated = true })
	}}

	cmd := Command(params.Command[0], params.Command[1:]...)
	cmd.Dir = params.Dir
	// ERUN_JOB_ID always rides along, not only when the caller sets Env: it is
	// what lets a nested `job start` run from inside this work (agent-gate.sh's
	// detach-and-await, or an agent driving it directly) record this job as its
	// StartedByJobID, so this job's own finish check can tell that nested job
	// apart from an unrelated one sharing the environment.
	cmd.Env = append(append(os.Environ(), sortedEnvironmentJobEnvPairs(params.Env)...), environmentJobIDEnvVar+"="+job.ID)
	cmd.Stdin = nil
	cmd.Stdout = writer
	cmd.Stderr = writer
	// The work runs in its own process group so a cancel can reach it and every
	// process it spawned without touching this supervisor — the bookkeeping has
	// to survive to report what happened.
	detachEnvironmentJobChild(cmd)

	if startErr := cmd.Start(); startErr != nil {
		return finishEnvironmentJob(recorder, beat, writer, 0, nil, startErr)
	}
	recorder.update(func(job *EnvironmentJob) { job.ChildPID = cmd.Process.Pid })

	waitErr := cmd.Wait()
	return finishEnvironmentJob(recorder, beat, writer, cmd.Process.Pid, cmd.ProcessState, waitErr)
}

// finishEnvironmentJob records the outcome the supervisor observed. This is the
// only place an exit status is ever produced, and it comes from waiting on the
// process — never from parsing output, and never from a shell's $?.
//
// childPID is the process cmd.Wait just reaped. detachEnvironmentJobChild put
// it in a process group named after its own pid, so a live process still in
// that group after the leader is gone — work the leader backgrounded and
// never waited for — is answered here, not left for the exit code alone to
// misreport as a clean success.
//
// captureAgentJobWorktreeOutcome runs here too, before the exit code alone
// could be read as the whole story: an agent job's own working tree is
// something its exit status says nothing about at all (see job_worktree.go).
//
// reclaimAgentJobWorkClone needs a snapshot that already carries this job's
// own final EnvironmentJobStateExited (or abandoned/gate-incomplete, which it
// refuses) rather than the "running" state the job still carried when
// finishEnvironmentJob was entered (see work_clone_reclaim.go for why that
// ordering matters) -- but it must also run, and any removal it decides on
// must actually finish, before that terminal state is written anywhere a
// caller can observe it. job.Finished() (what await/status poll on) is true
// the instant State reaches a terminal value, so settling State and
// CloneReclaimed/CloneKeptReason in two separate recorder.update calls left a
// window where a poll could read "finished" off the first write while the
// clone was still fully present on disk -- a caller-visible false success,
// not just a stale field. settle is applied to an in-memory copy first (no
// disk write), reclaim is decided and acted on against that settled copy, and
// only then does the single recorder.update below make any of it durable.
func finishEnvironmentJob(recorder *jobRecorder, beat *jobHeartbeat, writer *jobOutputWriter, childPID int, state *os.ProcessState, waitErr error) error {
	// Fold the stream's tail before the outcome lands, so the finished record
	// carries what the run last did rather than the poll's stale view of it.
	beat.refresh(false)

	code, signal, reason, jobState, startedJobFailed := resolveEnvironmentJobOutcome(recorder, childPID, state, waitErr)
	worktree := captureAgentJobWorktreeOutcome(recorder.snapshot())
	// Captured once resolveEnvironmentJobOutcome returns, before the reclaim
	// decision runs, so EndedAt reflects when this job's own outcome was
	// actually settled — which, for a job that waited out one it started (see
	// resolveEnvironmentJobOutcome), can be well after its own process exited
	// — rather than drifting later still by however long reclaim's own git
	// plumbing and removal take.
	endedAt := time.Now()
	settle := func(job *EnvironmentJob) {
		job.State = jobState
		job.EndedAt = endedAt
		job.OutputBytes = writer.written()
		job.OutputTruncated = writer.truncated()
		job.Signal = signal
		if reason == "" && code != 0 && job.Kind == EnvironmentJobKindAgent && job.Progress != nil {
			reason = agentAuthFailureReason(job.AgentTool, job.Progress.Error)
		}
		job.Reason = reason
		job.ExitCode = &code
		job.StartedJobFailed = startedJobFailed
		worktree.apply(job)
	}

	settled := recorder.snapshot()
	settle(&settled)
	reclaimed, cloneReason := reclaimAgentJobWorkClone(settled)

	recorder.update(func(job *EnvironmentJob) {
		settle(job)
		job.CloneReclaimed = reclaimed
		job.CloneKeptReason = cloneReason
	})
	return nil
}

// resolveEnvironmentJobOutcome decides the exit code, signal, reason,
// terminal state, and started-job failure finishEnvironmentJob records. State
// and reason can be overridden by work the job left behind that it never
// waited for: a process still alive in its own process group (abandoned) is
// decided immediately, since nothing about an orphaned process group member
// is worth waiting on. A sibling job record naming this job as its
// StartedByJobID is different: rather than declaring the outcome incomplete
// on the spot, this waits for it (see awaitEnvironmentJobRunningChildren) —
// the whole motivation being that a caller reading this job's own record
// should not have to separately chase down and await what it started merely
// because this job's own process happened to exit first. Only once that wait
// times out does gate-incomplete apply; if it ends because the started job
// finished, its failure (if any) is folded into startedJobFailed instead, so
// a clean exit code from this job's own process never overshadows a real
// failure in work it waited for.
func resolveEnvironmentJobOutcome(recorder *jobRecorder, childPID int, state *os.ProcessState, waitErr error) (code int, signal, reason, jobState, startedJobFailed string) {
	code = -1
	switch {
	case state != nil:
		code = state.ExitCode()
		if signal = environmentJobExitSignal(state); signal != "" {
			reason = "terminated by signal " + signal
		}
	case waitErr != nil:
		reason = "failed to start: " + waitErr.Error()
	}
	jobState = EnvironmentJobStateExited
	if state != nil && environmentJobProcessGroupSurvivors(childPID) {
		jobState = EnvironmentJobStateAbandoned
		reason = "the job's own process exited, but it left other processes still running in its process group — background work it started and never waited for; nothing further will be reported for that work"
	}
	self := recorder.snapshot()
	if running := awaitEnvironmentJobRunningChildren(recorder.dir, self.ID, resolveEnvironmentJobGateIncompleteWaitCap()); len(running) > 0 {
		jobState = EnvironmentJobStateGateIncomplete
		reason = environmentJobGateIncompleteReason(running)
		return code, signal, reason, jobState, startedJobFailed
	}
	if failed := environmentJobFailedChildren(recorder.dir, self.ID, time.Now()); len(failed) > 0 {
		startedJobFailed = environmentJobFailedChildReason(failed)
	}
	return code, signal, reason, jobState, startedJobFailed
}

// awaitEnvironmentJobRunningChildren blocks until no non-handoff job started
// by parentID is still running, or cap elapses, returning whatever is still
// running at that point (nil once none are). Unlike AwaitEnvironmentJob, this
// has no caller holding a connection or a terminal open on the other end of
// it — the supervisor calling this is already a detached background process
// — so it polls internally for as long as cap allows instead of returning a
// "still running" answer for someone else to re-ask.
func awaitEnvironmentJobRunningChildren(dir, parentID string, waitCap time.Duration) []EnvironmentJob {
	poll := resolveEnvironmentJobGateIncompletePoll()
	deadline := time.Now().Add(waitCap)
	for {
		running := environmentJobRunningChildren(dir, parentID, time.Now())
		if len(running) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return running
		}
		remaining := time.Until(deadline)
		if remaining < poll {
			poll = remaining
		}
		time.Sleep(poll)
	}
}

// environmentJobGateIncompleteReason names the still-running job(s) a caller
// needs to await for a real outcome, so the reason is actionable rather than
// just a label.
func environmentJobGateIncompleteReason(running []EnvironmentJob) string {
	ids := make([]string, 0, len(running))
	for _, job := range running {
		ids = append(ids, job.ID)
	}
	noun := "job"
	if len(ids) != 1 {
		noun = "jobs"
	}
	return fmt.Sprintf("this job ended while the %s it started (%s) had not reached a verdict; await it directly for the real outcome instead of treating this job's own exit as the answer", noun, strings.Join(ids, ", "))
}

// environmentJobFailedChildReason names the job(s) this job started, waited
// for, and that did not succeed, so a caller reading this job's own failure
// knows which started job to look at rather than only that this job's own
// exit code (which can still read as clean) is not the whole story.
func environmentJobFailedChildReason(failed []EnvironmentJob) string {
	ids := make([]string, 0, len(failed))
	for _, job := range failed {
		ids = append(ids, job.ID)
	}
	noun := "job"
	if len(ids) != 1 {
		noun = "jobs"
	}
	return fmt.Sprintf("the %s this job started (%s) did not succeed", noun, strings.Join(ids, ", "))
}

// startEnvironmentJobAliveBeat stamps the job's alive fields immediately and
// then on a fixed cadence for as long as the supervisor runs, independent of
// the lease-renewal/progress ticker below: that one backs off to a 300s
// interval for a command job, which is 60x too slow to answer "is the
// supervisor still there" within the 5s bound a caller applies. Stopping it is
// best-effort like the activity lease — if the supervisor is killed outright,
// the beat simply stops, and that silence past EnvironmentJobAliveStaleMs is
// itself the failure signal a caller reads; nothing needs to be undone.
func startEnvironmentJobAliveBeat(recorder *jobRecorder) func() {
	beatOnce := func() {
		recorder.update(func(job *EnvironmentJob) {
			job.LastAliveAt = time.Now()
			job.AliveSeq++
		})
	}
	beatOnce()

	done := make(chan struct{})
	var once sync.Once
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		ticker := time.NewTicker(EnvironmentJobAliveHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				beatOnce()
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		stopped.Wait()
	}
}

// agentJobProgressInterval is how often an agent job's stream is folded. It is
// short because the whole point is that a caller polling for progress sees the
// current activity, not one from minutes ago; each tick reads only the bytes
// that arrived since the last one.
const agentJobProgressInterval = 2 * time.Second

// jobHeartbeat renews the job's activity lease while the work runs and, for an
// agent job, folds the tool's event stream into the record's progress. One
// ticker does both so the lease's name can carry the current activity — which is
// what lets the desktop report "editing <file>" instead of only "running".
type jobHeartbeat struct {
	tenant      string
	environment string
	ttl         time.Duration
	recorder    *jobRecorder
	// agent is nil for a command job, whose log is not an event stream.
	agent *agentProgressReader

	mu        sync.Mutex
	progress  AgentJobProgress
	leaseName string
	renewedAt time.Time
}

// startEnvironmentJobHeartbeat takes the lease immediately and keeps it renewed
// for as long as the supervisor runs. Releasing on the way out is best-effort by
// design: if this process is killed instead, the lease records this pid, so the
// lease store reclaims it on the next read exactly as it would for any other
// abandoned holder.
//
// agent is the same reader the output writer feeds, so the heartbeat's snapshot
// reflects everything the process wrote, not only what the output cap retained.
func startEnvironmentJobHeartbeat(tenant, environment string, recorder *jobRecorder, ttl time.Duration, agent *agentProgressReader) (*jobHeartbeat, func()) {
	job := recorder.snapshot()
	beat := &jobHeartbeat{tenant: tenant, environment: environment, ttl: ttl, recorder: recorder, agent: agent}
	interval := beat.leaseInterval()
	if agent != nil {
		interval = agentJobProgressInterval
	}
	beat.refresh(true)

	done := make(chan struct{})
	var once sync.Once
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				beat.refresh(false)
			}
		}
	}()
	return beat, func() {
		once.Do(func() { close(done) })
		// The release must not race a tick that is already renewing, or the lease
		// would outlive the supervisor and keep the environment reading as busy.
		stopped.Wait()
		_ = ReleaseEnvironmentActivityLease(tenant, environment, job.LeaseID)
	}
}

// refresh folds whatever the agent emitted since the last tick and renews the
// lease. The record is rewritten only when progress actually moved, so a quiet
// agent costs one file read per tick and nothing else.
func (h *jobHeartbeat) refresh(force bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	job := h.recorder.snapshot()
	name := job.Name
	if h.agent != nil {
		progress := h.agent.snapshot()
		if progress != h.progress {
			h.progress = progress
			h.recorder.update(func(job *EnvironmentJob) {
				folded := progress
				job.Progress = &folded
			})
		}
		if summary := progress.Summary(); summary != "" {
			name = job.Name + ": " + summary
		}
	}
	if !force && name == h.leaseName && time.Since(h.renewedAt) < h.leaseInterval() {
		return
	}
	_, _ = TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant:      h.tenant,
		Environment: h.environment,
		Name:        name,
		ID:          job.LeaseID,
		PID:         job.PID,
		TTL:         h.ttl,
	})
	h.leaseName = name
	h.renewedAt = time.Now()
}

// leaseInterval renews well inside the TTL, with a floor so a short TTL cannot
// turn the renewal into a busy loop.
func (h *jobHeartbeat) leaseInterval() time.Duration {
	interval := h.ttl / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	return interval
}

// jobOutputWriter captures the work's merged stdout and stderr up to the cap.
// Past the cap it stops writing and says so once, so a bounded log can never be
// mistaken for the whole story. agent, when set, is fed every byte regardless
// of the cap: an agent's progress fields are small and bounded on their own, so
// the log's disk cap must not be what silently freezes them.
type jobOutputWriter struct {
	file       *os.File
	limit      int64
	agent      *agentProgressReader
	onTruncate func()

	mu      sync.Mutex
	count   int64
	dropped bool
}

// Write feeds the agent reader (if any) with every byte, then applies the cap
// to what actually reaches the log file. The two are independent so the cap
// can bound the file without also bounding progress.
func (w *jobOutputWriter) Write(p []byte) (int, error) {
	if w.agent != nil {
		w.agent.feed(p)
	}
	return w.writeCapped(p)
}

func (w *jobOutputWriter) writeCapped(p []byte) (int, error) {
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
	// A task job's PID is this process's own -- there is no subprocess to fall
	// back to signalling, and falling back anyway would send the signal to
	// whatever is running this call instead of refusing outright.
	if job.Kind == EnvironmentJobKindTask {
		return CancelEnvironmentJobResult{}, fmt.Errorf("job %q is a background task job with no subprocess to signal; wait for it or let it finish", job.ID)
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

// ResolveErunExecutable finds the erun binary for a transport that is not itself
// that binary — the in-environment MCP server supervising a job, the desktop app
// wiring a per-env MCP proxy — so those callers get a clear failure rather than a
// launch that silently never happens. A sibling of the running program is
// preferred over PATH so a source build resolves the binary it was built beside
// instead of an unrelated installed one.
func ResolveErunExecutable() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ERUN_ERUN_BIN")); override != "" {
		return override, nil
	}
	name := ErunExecutableName()
	if sibling := erunExecutableNearRunningProgram(name); sibling != "" {
		return sibling, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("the erun executable was not found beside this program or on PATH: %w", err)
	}
	return path, nil
}

// ErunExecutableName is the on-disk file name of the erun binary for this host.
func ErunExecutableName() string {
	if runtime.GOOS == "windows" {
		return "erun.exe"
	}
	return "erun"
}

// erunExecutableNearRunningProgram searches beside the running program: the
// install layout puts every erun executable in one directory, and a source build
// puts them in each module's own bin/. It is not the mirror of how the CLI finds
// erun-app — that direction stops at the .app bundle, while a program running
// from inside one sits three levels below the directory its siblings share.
func erunExecutableNearRunningProgram(name string) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return erunExecutableNear(executable, name)
}

func erunExecutableNear(executable, name string) string {
	for _, root := range executableSiblingRoots(filepath.Dir(executable)) {
		for _, candidate := range []string{
			filepath.Join(root, name),
			filepath.Clean(filepath.Join(root, "..", "..", "erun-cli", "bin", name)),
		} {
			if candidate == executable {
				continue
			}
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

// executableSiblingRoots lists the directories an erun executable may sit in
// relative to the running program: its own directory, plus the directory holding
// the macOS .app bundle it runs inside, if any.
func executableSiblingRoots(dir string) []string {
	roots := []string{dir}
	if container := macOSBundleContainer(dir); container != "" {
		roots = append(roots, container)
	}
	return roots
}

// macOSBundleContainer returns the directory holding the <Name>.app bundle whose
// Contents/MacOS is dir, and "" for every other layout — so no other host's
// directory names can accidentally match the bundle shape.
func macOSBundleContainer(dir string) string {
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	bundle := filepath.Dir(contents)
	if filepath.Ext(bundle) != ".app" {
		return ""
	}
	return filepath.Dir(bundle)
}

// The validators below run before a caller picks where the work happens, so a
// malformed request is refused for what is wrong with it rather than for
// whatever the environment's edge happened to answer. A request that is invalid
// on its face is invalid whether or not the environment can be reached.

// ValidateEnvironmentJobStart checks a start request's target, handle, and work.
func ValidateEnvironmentJobStart(params StartEnvironmentJobParams) error {
	if _, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, params.Name, params.ID); err != nil {
		return err
	}
	if _, _, err := resolveEnvironmentJobWork(params.Agent, params.Prompt, params.Command); err != nil {
		return err
	}
	_, err := normalizeEnvironmentJobEnv(params.Env)
	return err
}

// ValidateEnvironmentJobAttach checks an attach request's target and handle.
func ValidateEnvironmentJobAttach(params AttachEnvironmentJobParams) error {
	_, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, params.Name, params.ID)
	return err
}

// ValidateEnvironmentJobAwaitTimeout enforces the bounded-wait ceiling.
func ValidateEnvironmentJobAwaitTimeout(timeout time.Duration) error {
	_, err := normalizeEnvironmentJobAwaitTimeout(timeout)
	return err
}
