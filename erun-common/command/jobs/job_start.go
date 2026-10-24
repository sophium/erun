package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// jobSupervisorReportTimeout bounds how long a start waits for the supervisor to
// register the job before calling the start failed.
const jobSupervisorReportTimeout = 10 * time.Second

// jobSupervisorStartupReadLimit bounds how much of a supervisor's own output a
// failed start reads back to name the cause with.
const jobSupervisorStartupReadLimit = 4 << 10

// jobSupervisorStartupQuoteLimit bounds the cause a failed start quotes, so a
// supervisor that died shouting does not turn the start error into a transcript.
const jobSupervisorStartupQuoteLimit = 240

// StartEnvironmentJob detaches the work and returns the handle to it.
func StartEnvironmentJob(ctx core.Context, params StartEnvironmentJobParams) (shared.EnvironmentJob, error) {
	params, err := params.normalize()
	if err != nil {
		return shared.EnvironmentJob{}, err
	}
	if err := ensureAgentJobToolAvailable(ctx, params.Agent); err != nil {
		return shared.EnvironmentJob{}, err
	}
	dir, err := shared.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return shared.EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(ctx, dir, params.ID); err != nil {
		return shared.EnvironmentJob{}, err
	}
	// Resolved before anything is spawned: a start that has lost the
	// environment must be refused, not handed a handle to a job that then has
	// to die. params.Exclusive becomes what this job actually holds, so the
	// supervisor argv and the recorded job agree with it.
	if params.Exclusive, err = resolveEnvironmentJobExclusivity(ctx, dir, params, time.Now()); err != nil {
		return shared.EnvironmentJob{}, err
	}

	logPath := environmentJobLogPath(dir, params.ID)
	args := environmentJobSupervisorArgs(params)
	ctx.Trace(fmt.Sprintf("job: detaching %q as job %s, output at %s", params.Name, params.ID, logPath))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s for the job's lifetime", environmentJobLeaseID(params.ID)))
	if params.Exclusive {
		ctx.Trace(fmt.Sprintf("job: holding exclusive claim %s on scope %q, refusing every other job start here while it runs", environmentJobExclusiveLeaseID(params.ID), activity.EnvironmentActivityLeaseScopeEnvironment))
	}
	ctx.TraceCommand(params.Dir, params.SupervisorPath, args...)
	if ctx.DryRun {
		return plannedEnvironmentJob(params, logPath), nil
	}
	return spawnEnvironmentJobSupervisor(dir, params, args)
}

// spawnEnvironmentJobSupervisor takes whatever the resolved plan says this job
// holds, detaches the supervisor, and returns only once that supervisor has
// registered the job — so a start either yields a handle that resolves or fails
// outright, never a handle to nothing. Every failure path here gives back an
// exclusivity claim this start took, so a failed start cannot strand the
// environment for the claim's whole TTL.
func spawnEnvironmentJobSupervisor(dir string, params StartEnvironmentJobParams, args []string) (shared.EnvironmentJob, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return shared.EnvironmentJob{}, err
	}
	if params.Exclusive {
		if _, err := takeEnvironmentJobExclusivityClaim(params, time.Now()); err != nil {
			return shared.EnvironmentJob{}, environmentJobExclusivityTakeError(params, err, time.Now())
		}
	}
	cmd := core.Command(params.SupervisorPath, args...)
	// The supervisor inherits nothing from the caller's terminal: no working
	// directory it could pin, no stdio it could block on, and its own session, so
	// the transport dropping or the caller exiting cannot take it with them.
	cmd.Dir = ""
	cmd.Stdin = nil
	// Its own output is the exception, because it is the only account of a
	// supervisor that ends before registering anything: there is no record to
	// read a cause from, and the process itself is gone. Send it to the job's
	// log -- which a supervisor that does reach registration truncates again on
	// its way in -- rather than to the null device, so the start failure below
	// can name what happened instead of only that nothing did.
	startup := openEnvironmentJobStartupCapture(dir, params.ID)
	if startup != nil {
		cmd.Stdout = startup
		cmd.Stderr = startup
	}
	core.DetachEnvironmentJobSupervisor(cmd)
	startErr := cmd.Start()
	closeEnvironmentJobStartupCapture(startup)
	if startErr != nil {
		releaseUnsupervisedEnvironmentJobExclusivityClaim(params, 0)
		return shared.EnvironmentJob{}, fmt.Errorf("start job supervisor: %w", startErr)
	}
	// Reap the supervisor when it eventually exits. A long-lived caller (the MCP
	// server) would otherwise collect a zombie per job.
	go func() { _ = cmd.Wait() }()

	job, err := awaitEnvironmentJobRecord(dir, params.ID, cmd.Process.Pid)
	if err != nil {
		releaseUnsupervisedEnvironmentJobExclusivityClaim(params, cmd.Process.Pid)
		return shared.EnvironmentJob{}, err
	}
	return job, nil
}

// openEnvironmentJobStartupCapture opens the file a detached supervisor's own
// output is captured to, or returns nil when it cannot be opened at all: a start
// must never fail because its diagnostics could not be arranged, and a nil
// writer leaves the supervisor on the null device exactly as it always was.
//
// Truncating is the same decision reserveEnvironmentJobID already took -- this
// start owns the id, and any record and log under it belonged to finished work --
// and it is what makes the file unambiguously this attempt's, so a reader can
// never quote an earlier run's leftovers back as this death's cause.
func openEnvironmentJobStartupCapture(dir, id string) *os.File {
	file, err := os.OpenFile(environmentJobLogPath(dir, id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return file
}

// closeEnvironmentJobStartupCapture drops this process's copy of the capture
// file once the supervisor holds its own. The supervisor survives this call.
func closeEnvironmentJobStartupCapture(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

// jobSupervisorStartupFailureCause reads back what the supervisor process itself
// wrote before it died, so a start that failed before any record existed reports
// why the supervisor ended rather than only that it is gone.
//
// The file it reads was truncated by this start before the supervisor was
// spawned and is truncated again by any supervisor that reaches registration, so
// what it finds belongs to this attempt. Empty is a real answer and the one this
// returns for a supervisor killed outright, which gets no chance to say anything
// -- the bare "exited without registering" is then the whole truth, and nothing
// here may invent a cause to fill that gap.
func jobSupervisorStartupFailureCause(dir, id string) string {
	data, _, err := readEnvironmentJobLogPage(environmentJobLogPath(dir, id), 0, jobSupervisorStartupReadLimit)
	if err != nil {
		return ""
	}
	// Collapsed to one line, because the start error is one line and a
	// supervisor's dying words are routinely several.
	text := strings.Join(strings.Fields(string(data)), " ")
	if text == "" {
		return ""
	}
	if runes := []rune(text); len(runes) > jobSupervisorStartupQuoteLimit {
		text = string(runes[:jobSupervisorStartupQuoteLimit]) + "..."
	}
	return ": " + text
}

// releaseUnsupervisedEnvironmentJobExclusivityClaim drops a claim this start
// took for a supervisor that then failed to come up, so a failed start never
// strands the environment for the claim's whole TTL. It refuses to release
// while that supervisor is in fact alive: a start can fail to *observe* the
// job's registration and still have left a real supervisor running, and taking
// the environment out from under it would be worse than the failed start.
func releaseUnsupervisedEnvironmentJobExclusivityClaim(params StartEnvironmentJobParams, supervisorPID int) {
	if !params.Exclusive {
		return
	}
	if supervisorPID > 0 && activity.ProcessAlive(supervisorPID) {
		return
	}
	_ = releaseEnvironmentJobExclusivityClaim(params.Tenant, params.Environment, params.ID)
}

// reserveEnvironmentJobID makes the id unambiguous before anything is spawned.
// Reusing the id of work that is still running would leave two supervisors
// writing one record, so it is refused; reusing a finished one replaces it, and
// says so, because the alternative is an orchestrator unable to re-run named
// work without inventing ids.
func reserveEnvironmentJobID(ctx core.Context, dir, id string) error {
	existing, ok := readReservedEnvironmentJob(dir, id)
	if !ok {
		return nil
	}
	resolved := reconcileEnvironmentJob(dir, existing, time.Now(), activity.ProcessAlive, build.CurrentJobHostname())
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

// readReservedEnvironmentJob reads the record already under id. An unreadable
// record is treated as none: nothing then holds the id.
func readReservedEnvironmentJob(dir, id string) (shared.EnvironmentJob, bool) {
	job, err := shared.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
	return job, err == nil
}

// awaitEnvironmentJobRecord waits for the supervisor to register the job *and*
// to name the work's own process, so a start either returns a handle that
// resolves or fails outright — never a handle to nothing, and never a handle
// whose process cancel cannot name. The supervisor publishes its running record
// before it spawns the work, so a reader that stopped at the record's first
// appearance could observe a running job with no child pid; `job cancel` reads
// the same record, and with nothing else to name it would signal the
// supervisor, the one process that has to survive to record the outcome. A
// terminal record is a complete answer too: there is no work to name because
// there is no longer any work.
func awaitEnvironmentJobRecord(dir, id string, supervisorPID int) (shared.EnvironmentJob, error) {
	deadline := time.Now().Add(jobSupervisorReportTimeout)
	for {
		job, err := shared.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
		if err == nil && (job.ChildPID > 0 || job.Finished()) {
			return job, nil
		}
		if !time.Now().Before(deadline) {
			if err == nil {
				return shared.EnvironmentJob{}, fmt.Errorf("job supervisor %d registered job %q without naming the work's process within %s", supervisorPID, id, jobSupervisorReportTimeout)
			}
			return shared.EnvironmentJob{}, fmt.Errorf("job supervisor %d did not register job %q within %s", supervisorPID, id, jobSupervisorReportTimeout)
		}
		if !activity.ProcessAlive(supervisorPID) {
			// The supervisor is gone and there is no record to read a cause
			// from. Its own output is the one place that cause can survive, so
			// the failure to register is reported with it rather than alone.
			return shared.EnvironmentJob{}, fmt.Errorf("job supervisor %d exited without registering job %q%s", supervisorPID, id, jobSupervisorStartupFailureCause(dir, id))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func plannedEnvironmentJob(params StartEnvironmentJobParams, logPath string) shared.EnvironmentJob {
	return shared.EnvironmentJob{
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
		Exclusive:        params.Exclusive,
	}
}

// environmentJobSupervisorArgs is the one place the two halves of a job agree
// on how the supervisor is invoked. It lives beside the supervisor body so a
// change to either is a change to both. The path is `exec job supervise`
// regardless of which entry point started the job -- `erun job start` and its
// deprecated top-level alias both resolve to the same canonical re-exec path.
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
	if params.Exclusive {
		args = append(args, "--exclusive")
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
