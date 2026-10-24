package execcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

func newJobStatusCmd(resolveOpen shared.OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report one job's state and outcome, or every retained job",
		Long: "Answers running, exited, abandoned, gate-incomplete, or unknown — never a\n" +
			"partial answer. A job whose supervisor is gone without an outcome reads as\n" +
			"unknown rather than as success, which is what makes it safe to act on:\n" +
			"unknown, abandoned, and gate-incomplete are exactly as terminal as exited,\n" +
			"never a reason to keep waiting and never a success whatever the raw exit\n" +
			"code says. gate-incomplete means this job's own process ended while a job\n" +
			"it started had not reached a verdict; abandoned means it left other work it\n" +
			"started running in its own process group, unsupervised. A job's own\n" +
			"process exiting is therefore never the verdict on its own -- this job's own\n" +
			"eventual state is, including for a nested job it started after that process\n" +
			"already exited: it waits for that job before reporting exited, folding a\n" +
			"failure into startedJobFailed rather than letting its own clean exit code\n" +
			"hide it. Finished jobs stay readable for 24 hours so a caller that\n" +
			"reconnects after the work ended can still learn what happened.\n\n" +
			"For an agent job, gate-incomplete or startedJobFailed triggers one further\n" +
			"thing before either is reported as final: a bounded, automatic resumption of\n" +
			"the same session with the concrete outcome, so the agent gets a real chance\n" +
			"to act on it. reinvocationCount says how many of those already ran; once the\n" +
			"bound is reached the reason says so explicitly rather than reading the same\n" +
			"as a job that never got one.\n\n" +
			"aliveAgeMs is the milliseconds since the supervisor's last ~1s beat,\n" +
			"computed in erun's own clock so nothing subtracts a pod timestamp from a\n" +
			"caller's clock. Once it exceeds 5000, treat the job as failed (an unknown\n" +
			"outcome, never a success, never a tool error) even if state has not caught\n" +
			"up yet; a silent-but-healthy command never trips this, since the beat does\n" +
			"not depend on the work's own output.",
		Example: "  erun exec job status --tenant team --environment dev\n" +
			"  erun exec job status --tenant team --environment dev --id suite --output json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobStatus(cmd, resolveOpen, tenant, environment, id)
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job to report; omit for every retained job")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runJobStatus(cmd *cobra.Command, resolveOpen shared.OpenResolver, tenant, environment, id string) error {
	if err := validateJobTarget(tenant, environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	single := strings.TrimSpace(id) != ""
	job, jobs, resolved, err := readJobStatus(cmd.Context(), ctx, resolveOpen, tenant, environment, id)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if single {
		if ctx.Output != core.OutputJSON {
			if _, err := fmt.Fprintln(ctx.Stdout, jobStatusLine(job)); err != nil {
				return err
			}
		}
		return ctx.WriteResult(job)
	}
	if ctx.Output != core.OutputJSON {
		if err := writeJobList(ctx, jobs); err != nil {
			return err
		}
	}
	return ctx.WriteResult(jobs)
}

func readJobStatus(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, tenant, environment, id string) (sharedpkg.EnvironmentJob, []sharedpkg.EnvironmentJob, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return readJobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	}
	return readStoredJobStatus(commandCtx, tenant, environment, id)
}

func readJobStatusFromEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, tenant, environment, id string) (sharedpkg.EnvironmentJob, []sharedpkg.EnvironmentJob, bool, error) {
	result, resolved, err := jobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	if err != nil || !resolved {
		return sharedpkg.EnvironmentJob{}, nil, resolved, err
	}
	jobs := result.Jobs
	if jobs == nil {
		jobs = []sharedpkg.EnvironmentJob{}
	}
	if strings.TrimSpace(id) == "" {
		return sharedpkg.EnvironmentJob{}, jobs, resolved, nil
	}
	if result.Job == nil {
		return sharedpkg.EnvironmentJob{}, jobs, resolved, fmt.Errorf("%s/%s returned no job for id %q", tenant, environment, id)
	}
	return *result.Job, jobs, resolved, nil
}

func readStoredJobStatus(commandCtx core.Context, tenant, environment, id string) (sharedpkg.EnvironmentJob, []sharedpkg.EnvironmentJob, bool, error) {
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "status", tenant, environment, id)
		return sharedpkg.EnvironmentJob{}, nil, false, nil
	}
	now := time.Now()
	if strings.TrimSpace(id) != "" {
		job, err := jobspkg.LoadEnvironmentJob(tenant, environment, id, now)
		if err != nil {
			return sharedpkg.EnvironmentJob{}, nil, false, err
		}
		return job, []sharedpkg.EnvironmentJob{job}, true, nil
	}
	jobs, err := jobspkg.LoadEnvironmentJobs(tenant, environment, now)
	if err != nil {
		return sharedpkg.EnvironmentJob{}, nil, false, err
	}
	if jobs == nil {
		jobs = []sharedpkg.EnvironmentJob{}
	}
	return sharedpkg.EnvironmentJob{}, jobs, true, nil
}

func writeJobList(ctx core.Context, jobs []sharedpkg.EnvironmentJob) error {
	if len(jobs) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no jobs")
		return err
	}
	for _, job := range jobs {
		if err := shared.WriteLabeledValue(ctx, job.ID, jobStatusLine(job)); err != nil {
			return err
		}
	}
	return nil
}

// jobStatusLine renders the outcome without ever implying one that was not
// recorded: an unknown job says so and says why.
func jobStatusLine(job sharedpkg.EnvironmentJob) string {
	switch job.State {
	case jobspkg.EnvironmentJobStateRunning:
		return jobRunningLine(job)
	case sharedpkg.EnvironmentJobStateExited:
		return jobExitedLine(job)
	case sharedpkg.EnvironmentJobStateAbandoned:
		return jobAbandonedLine(job)
	case sharedpkg.EnvironmentJobStateGateIncomplete:
		return jobGateIncompleteLine(job)
	default:
		return jobUnknownLine(job)
	}
}

func jobRunningLine(job sharedpkg.EnvironmentJob) string {
	line := fmt.Sprintf("running: %s", job.Name)
	if job.ChildPID > 0 {
		line += fmt.Sprintf(", pid %d", job.ChildPID)
	} else if job.PID > 0 {
		line += fmt.Sprintf(", pid %d", job.PID)
	}
	return line + jobExclusiveSuffix(job) + jobAliveSuffix(job) + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobOutputSuffix(job)
}

// jobExclusiveSuffix names the one thing that explains why an unrelated job
// start here is being refused right now. Without it an operator reading `job
// status` sees a running job and a refusal that mentions a lease id, with
// nothing on screen connecting the two.
func jobExclusiveSuffix(job sharedpkg.EnvironmentJob) string {
	if !job.Exclusive {
		return ""
	}
	return ", holding this environment exclusively"
}

// jobExitedLine renders the reason a clean exit cannot carry on its own. Every
// other terminal line already did; this one did not, so a failed job's recorded
// reason -- the only thing on the record that says what went wrong, and for a
// task job the only place the work's own error text lands at all -- was
// readable in JSON and invisible to anyone reading the human line. The signal
// takes its place when there is one: that already is the reason.
func jobExitedLine(job sharedpkg.EnvironmentJob) string {
	line := fmt.Sprintf("exited %d: %s", jobExitCodeOrUnset(job), job.Name)
	switch {
	case strings.TrimSpace(job.Signal) != "":
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	case strings.TrimSpace(job.Reason) != "":
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobCloneSuffix(job) + jobOutputSuffix(job)
}

// jobStartedJobFailedSuffix surfaces a job this job started and waited for
// (see EnvironmentJobStateGateIncomplete) that finished without succeeding --
// the one thing this job's own exit code cannot say once that wait is over.
func jobStartedJobFailedSuffix(job sharedpkg.EnvironmentJob) string {
	if strings.TrimSpace(job.StartedJobFailed) == "" {
		return ""
	}
	return ", " + job.StartedJobFailed
}

// jobCloneSuffix surfaces what happened to an agent job's own working
// directory now that it has finished: reclaimed once nothing would be lost by
// deleting it, or kept with the exact reason an operator would otherwise have
// to work out by hand from git state. Silent either way, a vanished directory
// and an accumulating one both read as the product doing nothing. See
// work_clone_reclaim.go in erun-common.
func jobCloneSuffix(job sharedpkg.EnvironmentJob) string {
	if job.CloneReclaimed {
		return ", clone reclaimed"
	}
	if strings.TrimSpace(job.CloneKeptReason) != "" {
		return ", clone kept: " + job.CloneKeptReason
	}
	return ""
}

// jobWorktreeSuffix surfaces a dirty working tree on any terminal line, so a
// job that otherwise looks like a clean success still shows the one thing its
// exit status cannot: uncommitted work in its own working directory, and
// whether the supervisor managed to check-point it. See job_worktree.go in
// erun-common for what this reports and why.
func jobWorktreeSuffix(job sharedpkg.EnvironmentJob) string {
	if !job.WorktreeDirty {
		return ""
	}
	return ", " + jobWorktreeSummary(job)
}

// jobWorktreeSummary is the human-readable half of jobWorktreeSuffix, reused
// by jobAwaitExit's error text so the two surfaces never describe the same
// outcome differently.
func jobWorktreeSummary(job sharedpkg.EnvironmentJob) string {
	switch {
	case job.WorktreePushed:
		return fmt.Sprintf("working tree was dirty; checkpointed as %s and pushed to %s", job.WorktreeCommit, job.WorktreeRemote)
	case job.WorktreeCommit != "":
		return fmt.Sprintf("working tree was dirty; checkpointed as %s but not pushed: %s", job.WorktreeCommit, job.WorktreeReason)
	default:
		return fmt.Sprintf("working tree was dirty and left uncommitted: %s", job.WorktreeReason)
	}
}

// jobAbandonedLine is rendered distinctly from both exited and unknown: the
// exit status was captured (unlike unknown), but it is never a success (unlike
// exited) -- the reason names the background work nothing will ever report on.
func jobAbandonedLine(job sharedpkg.EnvironmentJob) string {
	line := fmt.Sprintf("abandoned %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

// jobGateIncompleteLine is rendered distinctly from abandoned: the still-running
// work is a sibling job record this job started, not a process left behind in
// its own process group.
func jobGateIncompleteLine(job sharedpkg.EnvironmentJob) string {
	line := fmt.Sprintf("gate-incomplete %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

func jobUnknownLine(job sharedpkg.EnvironmentJob) string {
	line := fmt.Sprintf("unknown: %s", job.Name)
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobOutputSuffix(job)
}

// jobAliveSuffix surfaces the supervisor's alive beat for a running job, so an
// operator watching `job status` sees the same signal the 5s caller rule acts
// on rather than only the frozen activity string a stalled beat would
// otherwise leave on screen.
func jobAliveSuffix(job sharedpkg.EnvironmentJob) string {
	if job.AliveAgeMs == nil {
		return ""
	}
	return fmt.Sprintf(", last beat %dms ago", *job.AliveAgeMs)
}

// jobAgentSuffix reports what an agent run is doing right now. An agent job that
// has emitted nothing yet says so rather than reading as an idle one — the
// difference is exactly what a caller polling for progress needs.
func jobAgentSuffix(job sharedpkg.EnvironmentJob) string {
	if job.Kind != jobspkg.EnvironmentJobKindAgent {
		return ""
	}
	suffix := ", agent " + job.AgentTool
	if job.Progress == nil {
		return suffix + ", no events yet"
	}
	if summary := job.Progress.Summary(); summary != "" {
		return suffix + ", " + summary
	}
	return suffix + ", no events yet"
}

// jobReinvocationSuffix surfaces a job's automatic bounded resumptions, the
// one thing that turns "this job started work that failed" into a real second
// chance for the agent to act rather than a silent record nobody reads. Empty
// for a job that never needed one.
func jobReinvocationSuffix(job sharedpkg.EnvironmentJob) string {
	if job.ReinvocationCount <= 0 {
		return ""
	}
	return fmt.Sprintf(", resumed %d/%d time(s)", job.ReinvocationCount, jobspkg.EnvironmentJobMaxReinvocations())
}

func jobOutputSuffix(job sharedpkg.EnvironmentJob) string {
	suffix := fmt.Sprintf(", %d bytes of output", job.OutputBytes)
	if job.OutputTruncated {
		suffix += " (truncated at the output cap)"
	}
	return suffix
}

func jobExitCodeOrUnset(job sharedpkg.EnvironmentJob) int {
	if job.ExitCode == nil {
		return -1
	}
	return *job.ExitCode
}
