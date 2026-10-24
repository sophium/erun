package execcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envcall"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/spf13/cobra"
)

func newJobStatusCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
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
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func runJobStatus(cmd *cobra.Command, resolveOpen envscope.OpenResolver, tenant, environment, id string) error {
	if err := validateJobTarget(tenant, environment); err != nil {
		return err
	}
	ctx := clicmd.CommandContext(cmd)
	single := strings.TrimSpace(id) != ""
	job, jobs, resolved, err := readJobStatus(cmd.Context(), ctx, resolveOpen, tenant, environment, id)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if single {
		if ctx.Output != model.OutputJSON {
			if _, err := fmt.Fprintln(ctx.Stdout, jobStatusLine(job)); err != nil {
				return err
			}
		}
		return ctx.WriteResult(job)
	}
	if ctx.Output != model.OutputJSON {
		if err := writeJobList(ctx, jobs); err != nil {
			return err
		}
	}
	return ctx.WriteResult(jobs)
}

func readJobStatus(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment, id string) (jobstate.EnvironmentJob, []jobstate.EnvironmentJob, bool, error) {
	if !envcall.EnvironmentTargetsItself() {
		return readJobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	}
	return readStoredJobStatus(commandCtx, tenant, environment, id)
}

func readJobStatusFromEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, tenant, environment, id string) (jobstate.EnvironmentJob, []jobstate.EnvironmentJob, bool, error) {
	result, resolved, err := jobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	if err != nil || !resolved {
		return jobstate.EnvironmentJob{}, nil, resolved, err
	}
	jobs := result.Jobs
	if jobs == nil {
		jobs = []jobstate.EnvironmentJob{}
	}
	if strings.TrimSpace(id) == "" {
		return jobstate.EnvironmentJob{}, jobs, resolved, nil
	}
	if result.Job == nil {
		return jobstate.EnvironmentJob{}, jobs, resolved, fmt.Errorf("%s/%s returned no job for id %q", tenant, environment, id)
	}
	return *result.Job, jobs, resolved, nil
}

func readStoredJobStatus(commandCtx core.Context, tenant, environment, id string) (jobstate.EnvironmentJob, []jobstate.EnvironmentJob, bool, error) {
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "status", tenant, environment, id)
		return jobstate.EnvironmentJob{}, nil, false, nil
	}
	now := time.Now()
	if strings.TrimSpace(id) != "" {
		job, err := jobspkg.LoadEnvironmentJob(tenant, environment, id, now)
		if err != nil {
			return jobstate.EnvironmentJob{}, nil, false, err
		}
		return job, []jobstate.EnvironmentJob{job}, true, nil
	}
	jobs, err := jobspkg.LoadEnvironmentJobs(tenant, environment, now)
	if err != nil {
		return jobstate.EnvironmentJob{}, nil, false, err
	}
	if jobs == nil {
		jobs = []jobstate.EnvironmentJob{}
	}
	return jobstate.EnvironmentJob{}, jobs, true, nil
}

func writeJobList(ctx core.Context, jobs []jobstate.EnvironmentJob) error {
	if len(jobs) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no jobs")
		return err
	}
	for _, job := range jobs {
		if err := textfmt.WriteLabeledValue(ctx, job.ID, jobStatusLine(job)); err != nil {
			return err
		}
	}
	return nil
}

// jobStatusLine never implies an unrecorded outcome.
func jobStatusLine(job jobstate.EnvironmentJob) string {
	switch job.State {
	case jobspkg.EnvironmentJobStateRunning:
		return jobRunningLine(job)
	case jobstate.EnvironmentJobStateExited:
		return jobExitedLine(job)
	case jobstate.EnvironmentJobStateAbandoned:
		return jobAbandonedLine(job)
	case jobstate.EnvironmentJobStateGateIncomplete:
		return jobGateIncompleteLine(job)
	default:
		return jobUnknownLine(job)
	}
}

func jobRunningLine(job jobstate.EnvironmentJob) string {
	line := fmt.Sprintf("running: %s", job.Name)
	if job.ChildPID > 0 {
		line += fmt.Sprintf(", pid %d", job.ChildPID)
	} else if job.PID > 0 {
		line += fmt.Sprintf(", pid %d", job.PID)
	}
	return line + jobExclusiveSuffix(job) + jobAliveSuffix(job) + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobOutputSuffix(job)
}

// jobExclusiveSuffix links a running job to the lease refusing other starts.
func jobExclusiveSuffix(job jobstate.EnvironmentJob) string {
	if !job.Exclusive {
		return ""
	}
	return ", holding this environment exclusively"
}

// jobExitedLine includes the signal, else the recorded failure reason.
func jobExitedLine(job jobstate.EnvironmentJob) string {
	line := fmt.Sprintf("exited %d: %s", jobExitCodeOrUnset(job), job.Name)
	switch {
	case strings.TrimSpace(job.Signal) != "":
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	case strings.TrimSpace(job.Reason) != "":
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobCloneSuffix(job) + jobOutputSuffix(job)
}

// jobStartedJobFailedSuffix surfaces a waited-for child job that did not succeed.
func jobStartedJobFailedSuffix(job jobstate.EnvironmentJob) string {
	if strings.TrimSpace(job.StartedJobFailed) == "" {
		return ""
	}
	return ", " + job.StartedJobFailed
}

// jobCloneSuffix reports whether an agent job's clone was reclaimed or kept, and why.
func jobCloneSuffix(job jobstate.EnvironmentJob) string {
	if job.CloneReclaimed {
		return ", clone reclaimed"
	}
	if strings.TrimSpace(job.CloneKeptReason) != "" {
		return ", clone kept: " + job.CloneKeptReason
	}
	return ""
}

// jobWorktreeSuffix surfaces uncommitted work and whether it was check-pointed.
func jobWorktreeSuffix(job jobstate.EnvironmentJob) string {
	if !job.WorktreeDirty {
		return ""
	}
	return ", " + jobWorktreeSummary(job)
}

// jobWorktreeSummary is shared with jobAwaitExit so both describe it identically.
func jobWorktreeSummary(job jobstate.EnvironmentJob) string {
	switch {
	case job.WorktreePushed:
		return fmt.Sprintf("working tree was dirty; checkpointed as %s and pushed to %s", job.WorktreeCommit, job.WorktreeRemote)
	case job.WorktreeCommit != "":
		return fmt.Sprintf("working tree was dirty; checkpointed as %s but not pushed: %s", job.WorktreeCommit, job.WorktreeReason)
	default:
		return fmt.Sprintf("working tree was dirty and left uncommitted: %s", job.WorktreeReason)
	}
}

// jobAbandonedLine: the exit was captured but background work was left running.
func jobAbandonedLine(job jobstate.EnvironmentJob) string {
	line := fmt.Sprintf("abandoned %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

// jobGateIncompleteLine: the still-running work is a child job, not a leftover process.
func jobGateIncompleteLine(job jobstate.EnvironmentJob) string {
	line := fmt.Sprintf("gate-incomplete %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobReinvocationSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

func jobUnknownLine(job jobstate.EnvironmentJob) string {
	line := fmt.Sprintf("unknown: %s", job.Name)
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobOutputSuffix(job)
}

// jobAliveSuffix shows the supervisor's alive beat.
func jobAliveSuffix(job jobstate.EnvironmentJob) string {
	if job.AliveAgeMs == nil {
		return ""
	}
	return fmt.Sprintf(", last beat %dms ago", *job.AliveAgeMs)
}

// jobAgentSuffix distinguishes an agent that has emitted nothing from an idle one.
func jobAgentSuffix(job jobstate.EnvironmentJob) string {
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

func jobReinvocationSuffix(job jobstate.EnvironmentJob) string {
	if job.ReinvocationCount <= 0 {
		return ""
	}
	return fmt.Sprintf(", resumed %d/%d time(s)", job.ReinvocationCount, jobspkg.EnvironmentJobMaxReinvocations())
}

func jobOutputSuffix(job jobstate.EnvironmentJob) string {
	suffix := fmt.Sprintf(", %d bytes of output", job.OutputBytes)
	if job.OutputTruncated {
		suffix += " (truncated at the output cap)"
	}
	return suffix
}

func jobExitCodeOrUnset(job jobstate.EnvironmentJob) int {
	if job.ExitCode == nil {
		return -1
	}
	return *job.ExitCode
}
