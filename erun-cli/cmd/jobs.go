package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newJobsCmd builds `erun jobs`: the platform-backed view of what agents and
// orchestrators are working on right now, and what recently finished. It is
// the host-side counterpart to `erun gate list`, and it answers the question
// the environment-scoped `erun exec job` verbs cannot -- those observe work in
// one environment, this observes the tenant's whole queue, including work that
// never entered an environment at all.
//
// It is deliberately its own top-level group rather than a verb on `erun exec
// job`: that group is scoped to one environment's own jobs, and a platform
// queue read is not. `erun job` is the deprecated alias for that group and is
// on its way out, so nothing new is added under it.
func newJobsCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := newCommandGroup(
		"jobs",
		"See what agents and orchestrators are working on, and what recently finished",
		newJobsListCmd(store, &alias, deps),
		newJobsShowCmd(store, &alias, deps),
		newJobsStartCmd(store, &alias, deps),
		newJobsFinishCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newJobsListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.JobListParams
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs: what is being worked on now, and what recently finished",
		Long: "List jobs, the live queue first, narrowed by any combination of the filters below. Each entry " +
			"names what is being done, by whom, and how long it has been going.\n\n" +
			"A RUNNING job is work in flight. ABANDONED means its actor stopped updating it and the platform " +
			"swept it -- read it as dropped, not as failed.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun jobs list --status RUNNING\n  erun jobs list --issue sophium/erun#2109",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			jobs, err := common.RunJobList(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun jobs list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformJobList(ctx, jobs); err != nil {
					return err
				}
			}
			return ctx.WriteResult(jobs)
		},
	}
	cmd.Flags().StringVar(&params.Status, "status", "", "Filter by status: RUNNING, SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED")
	cmd.Flags().StringVar(&params.EnvironmentID, "environment-id", "", "Filter by the platform's environment id")
	cmd.Flags().StringVar(&params.IssueRef, "issue", "", "Filter by the issue the work belongs to, e.g. owner/repo#2109")
	cmd.Flags().StringVar(&params.Scope, "scope", "", "Filter by the scope a job claims")
	cmd.Flags().StringVar(&params.ActorID, "actor", "", "Filter by the actor holding the job")
	addDryRunFlag(cmd)
	return cmd
}

func newJobsShowCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show <job-id>",
		Short:        "Show one job in full",
		Long:         "Show one job by its platform id, including the scope it claims and what it mirrors in the pod.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			job, err := common.RunJobShow(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun jobs show planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writePlatformJobDetail(ctx, job); err != nil {
					return err
				}
			}
			return ctx.WriteResult(job)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newJobsStartCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.JobClaimParams
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Record that this actor is starting a piece of work",
		Long: "Record a job starting, so the queue shows what is in flight before it finishes and a second " +
			"actor can see it.\n\n" +
			"With --scope set this is a claim: if another open job already holds that scope, this fails with " +
			"409 naming who holds it, what they are doing, and since when -- so you can pick up something " +
			"else instead of duplicating the work.\n\n" +
			"The summary is prose describing the work, never the command that performs it.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun jobs start --type fix --issue sophium/erun#2109 --scope sophium/erun#2109 \\\n" +
			"    --summary \"fixing the jobs claim race\" --actor erun/code4",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			job, err := common.RunJobClaim(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun jobs start planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "Started job %s (%s, %s).\n", job.JobID, job.JobType, job.Status); err != nil {
					return err
				}
			}
			return ctx.WriteResult(job)
		},
	}
	cmd.Flags().StringVar(&params.JobType, "type", "", "Job type: fix, review, gate, release, deploy, investigate, plan, triage, or maintenance")
	cmd.Flags().StringVar(&params.Summary, "summary", "", "What the work is, in prose (not the command that performs it)")
	cmd.Flags().StringVar(&params.ActorID, "actor", "", "Who is doing the work: the orchestrator id or agent identity")
	cmd.Flags().StringVar(&params.ActorKind, "actor-kind", "agent", "What kind of actor this is: agent, orchestrator, or human")
	cmd.Flags().StringVar(&params.Environment, "environment", "", "Local environment name this work runs in (omit for host-side work)")
	cmd.Flags().StringVar(&params.IssueRef, "issue", "", "The issue this work belongs to, e.g. owner/repo#2109")
	cmd.Flags().StringVar(&params.Scope, "scope", "", "What this job claims, so a second actor is told who holds it")
	cmd.Flags().StringVar(&params.LocalJobID, "local-job-id", "", "The in-pod job id this mirrors, when there is one")
	addDryRunFlag(cmd)
	return cmd
}

func newJobsFinishCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.JobUpdateParams
	cmd := &cobra.Command{
		Use:   "finish <job-id>",
		Short: "Report a job's progress or its outcome",
		Long: "Report how a job ended (or refresh what it is doing). A job that has already finished cannot be " +
			"updated: its outcome is the record coordination and reporting both read.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun jobs finish job_01H... --status SUCCEEDED\n  erun jobs finish job_01H... --summary \"waiting on the gate build\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			params.JobID = args[0]
			job, err := common.RunJobUpdate(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun jobs finish planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "Updated job %s (%s).\n", job.JobID, job.Status); err != nil {
					return err
				}
			}
			return ctx.WriteResult(job)
		},
	}
	cmd.Flags().StringVar(&params.Status, "status", "", "Close the job as SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED")
	cmd.Flags().StringVar(&params.Summary, "summary", "", "Refresh what the job is doing, in prose")
	cmd.Flags().StringVar(&params.LocalJobID, "local-job-id", "", "Record the in-pod job id this mirrors")
	addDryRunFlag(cmd)
	return cmd
}

func writePlatformJobList(ctx common.Context, jobs []common.PlatformJob) error {
	if len(jobs) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no jobs")
		return err
	}
	for _, job := range jobs {
		if err := writePlatformJobLine(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

// writePlatformJobLine renders one row: what is being done, by whom, and how long it
// has been going. The summary is the row's whole point, so it leads; the job
// id trails so it can be copied into `erun jobs show`.
func writePlatformJobLine(ctx common.Context, job common.PlatformJob) error {
	suffix := ""
	if strings.TrimSpace(job.IssueRef) != "" {
		suffix += " issue=" + job.IssueRef
	}
	if strings.TrimSpace(job.Scope) != "" {
		suffix += " scope=" + job.Scope
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s %s by %s (%s)%s\n     id=%s\n",
		job.Status, job.Summary, job.ActorID, job.ActorKind, suffix, job.JobID)
	return err
}

func writePlatformJobDetail(ctx common.Context, job common.PlatformJob) error {
	ended := "-"
	if job.EndedAt != nil {
		ended = job.EndedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"job %s\n  status:      %s\n  type:        %s\n  summary:     %s\n"+
			"  actor:       %s (%s)\n  issue:       %s\n  scope:       %s\n"+
			"  environment: %s\n  local job:   %s\n  started:     %s\n  ended:       %s\n",
		job.JobID, job.Status, job.JobType, job.Summary,
		job.ActorID, job.ActorKind,
		orDash(job.IssueRef), orDash(job.Scope),
		orDash(job.EnvironmentID), orDash(job.LocalJobID),
		job.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), ended)
	return err
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
