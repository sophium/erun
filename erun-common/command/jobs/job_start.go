package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/primitive/build/jobhost"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

const jobSupervisorReportTimeout = 10 * time.Second

const jobSupervisorStartupReadLimit = 4 << 10

const jobSupervisorStartupQuoteLimit = 240

// StartEnvironmentJob detaches the work and returns the handle to it.
func StartEnvironmentJob(ctx core.Context, params StartEnvironmentJobParams) (jobstate.EnvironmentJob, error) {
	params, err := params.normalize()
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if err := ensureAgentJobToolAvailable(ctx, params.Agent); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	dir, err := jobexclusive.EnvironmentJobDir(params.Tenant, params.Environment)
	if err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if err := reserveEnvironmentJobID(ctx, dir, params.ID); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	// Resolve exclusivity before spawning so a lost environment is refused synchronously.
	if params.Exclusive, err = resolveEnvironmentJobExclusivity(ctx, dir, params, time.Now()); err != nil {
		return jobstate.EnvironmentJob{}, err
	}

	logPath := environmentJobLogPath(dir, params.ID)
	args := environmentJobSupervisorArgs(params)
	ctx.Trace(fmt.Sprintf("job: detaching %q as job %s, output at %s", params.Name, params.ID, logPath))
	ctx.Trace(fmt.Sprintf("job: holding activity lease %s for the job's lifetime", environmentJobLeaseID(params.ID)))
	if params.Exclusive {
		ctx.Trace(fmt.Sprintf("job: holding exclusive claim %s on scope %q, refusing every other job start here while it runs", environmentJobExclusiveLeaseID(params.ID), activitylease.EnvironmentActivityLeaseScopeEnvironment))
	}
	ctx.TraceCommand(params.Dir, params.SupervisorPath, args...)
	if ctx.DryRun {
		return plannedEnvironmentJob(params, logPath), nil
	}
	return spawnEnvironmentJobSupervisor(dir, params, args)
}

// spawnEnvironmentJobSupervisor returns once the supervisor has registered the
// job; every failure releases any exclusivity claim this start took.
func spawnEnvironmentJobSupervisor(dir string, params StartEnvironmentJobParams, args []string) (jobstate.EnvironmentJob, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return jobstate.EnvironmentJob{}, err
	}
	if params.Exclusive {
		if _, err := takeEnvironmentJobExclusivityClaim(params, time.Now()); err != nil {
			return jobstate.EnvironmentJob{}, environmentJobExclusivityTakeError(params, err, time.Now())
		}
	}
	cmd := core.Command(params.SupervisorPath, args...)
	// Detach from the caller's terminal so its exit cannot take the supervisor with it.
	cmd.Dir = ""
	cmd.Stdin = nil
	// Capture the supervisor's own output in the job log so a death before
	// registration can name its cause.
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
		return jobstate.EnvironmentJob{}, fmt.Errorf("start job supervisor: %w", startErr)
	}
	// Reap the supervisor so a long-lived caller does not collect zombies.
	go func() { _ = cmd.Wait() }()

	job, err := awaitEnvironmentJobRecord(dir, params.ID, cmd.Process.Pid)
	if err != nil {
		releaseUnsupervisedEnvironmentJobExclusivityClaim(params, cmd.Process.Pid)
		return jobstate.EnvironmentJob{}, err
	}
	return job, nil
}

// openEnvironmentJobStartupCapture truncates the job log for this attempt, or
// returns nil; a start must never fail over its own diagnostics.
func openEnvironmentJobStartupCapture(dir, id string) *os.File {
	file, err := os.OpenFile(environmentJobLogPath(dir, id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return file
}

func closeEnvironmentJobStartupCapture(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

// jobSupervisorStartupFailureCause quotes what the supervisor wrote before dying;
// empty when it was killed outright, and nothing is invented.
func jobSupervisorStartupFailureCause(dir, id string) string {
	data, _, err := readEnvironmentJobLogPage(environmentJobLogPath(dir, id), 0, jobSupervisorStartupReadLimit)
	if err != nil {
		return ""
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	if text == "" {
		return ""
	}
	if runes := []rune(text); len(runes) > jobSupervisorStartupQuoteLimit {
		text = string(runes[:jobSupervisorStartupQuoteLimit]) + "..."
	}
	return ": " + text
}

// releaseUnsupervisedEnvironmentJobExclusivityClaim drops a failed start's claim,
// unless the supervisor is in fact alive.
func releaseUnsupervisedEnvironmentJobExclusivityClaim(params StartEnvironmentJobParams, supervisorPID int) {
	if !params.Exclusive {
		return
	}
	if supervisorPID > 0 && activitylease.ProcessAlive(supervisorPID) {
		return
	}
	_ = releaseEnvironmentJobExclusivityClaim(params.Tenant, params.Environment, params.ID)
}

// reserveEnvironmentJobID refuses an id still running and replaces a finished one.
func reserveEnvironmentJobID(ctx core.Context, dir, id string) error {
	existing, ok := readReservedEnvironmentJob(dir, id)
	if !ok {
		return nil
	}
	resolved := reconcileEnvironmentJob(dir, existing, time.Now(), activitylease.ProcessAlive, jobhost.CurrentJobHostname())
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

// readReservedEnvironmentJob treats an unreadable record as none.
func readReservedEnvironmentJob(dir, id string) (jobstate.EnvironmentJob, bool) {
	job, err := jobstate.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
	return job, err == nil
}

// awaitEnvironmentJobRecord waits until the record names the work's pid (or is
// terminal); otherwise cancel could only signal the supervisor itself.
func awaitEnvironmentJobRecord(dir, id string, supervisorPID int) (jobstate.EnvironmentJob, error) {
	deadline := time.Now().Add(jobSupervisorReportTimeout)
	for {
		job, err := jobstate.ReadEnvironmentJob(filepath.Join(dir, id+".json"))
		if err == nil && (job.ChildPID > 0 || job.Finished()) {
			return job, nil
		}
		if !time.Now().Before(deadline) {
			if err == nil {
				return jobstate.EnvironmentJob{}, fmt.Errorf("job supervisor %d registered job %q without naming the work's process within %s", supervisorPID, id, jobSupervisorReportTimeout)
			}
			return jobstate.EnvironmentJob{}, fmt.Errorf("job supervisor %d did not register job %q within %s", supervisorPID, id, jobSupervisorReportTimeout)
		}
		if !activitylease.ProcessAlive(supervisorPID) {
			return jobstate.EnvironmentJob{}, fmt.Errorf("job supervisor %d exited without registering job %q%s", supervisorPID, id, jobSupervisorStartupFailureCause(dir, id))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func plannedEnvironmentJob(params StartEnvironmentJobParams, logPath string) jobstate.EnvironmentJob {
	return jobstate.EnvironmentJob{
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

// environmentJobSupervisorArgs builds the `exec job supervise` argv.
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
