package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

// The job verbs start long work in an environment and observe it by handle. The
// work, its log, and the activity lease that keeps idle-stop off it all live in
// the environment: inside it these act on its store directly, and anywhere else
// they go to its own edge. What none of them re-implement is detachment, log
// redirection, polling, or exit-status capture around a general-purpose escape
// hatch, because every one of those is a place to get it wrong.

const (
	// jobAwaitTimeoutExitCode is the bounded wait elapsing with the work still
	// running. It is deliberately not 1: a caller that cannot tell "not finished
	// yet" from "failed" is the exact defect this surface closes, and 124 is what
	// timeout(1) has always meant.
	jobAwaitTimeoutExitCode = 124
	// jobAwaitUnknownExitCode is a job whose outcome was never recorded. Distinct
	// again, because "we do not know" must never be actioned as "it failed" or as
	// "it worked".
	jobAwaitUnknownExitCode = 125
)

func newJobCmd(resolveOpen OpenResolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Start long work in the environment and observe it by handle",
		Long: "Start detached work, then ask what happened to it: whether it is running, what\n" +
			"it exited with, and what it printed. Starting a job also holds an activity\n" +
			"lease for its lifetime, so the environment reports as busy and idle-stop\n" +
			"leaves it alone while the work runs.\n\n" +
			"The work always runs in the environment, never on the machine you type this\n" +
			"on: off-environment these verbs act through the environment's MCP edge, which\n" +
			"needs the port-forward `erun open` establishes. Paths are the environment's —\n" +
			"--dir and the reported log path resolve inside it.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(
		newJobStartCmd(resolveOpen),
		newJobAttachCmd(resolveOpen),
		newJobStatusCmd(resolveOpen),
		newJobAwaitCmd(resolveOpen),
		newJobOutputCmd(resolveOpen),
		newJobCancelCmd(resolveOpen),
		newJobSuperviseCmd(),
	)
	return cmd
}

func newJobStartCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var dir string
	var agent string
	var maxOutputBytes int64
	var leaseTTL time.Duration
	cmd := &cobra.Command{
		Use:   "start [flags] -- <command> [args...]",
		Short: "Run a command, or an AI agent, as a detached job and return its handle",
		Long: "The command keeps running after this call returns and after the caller's\n" +
			"connection drops: erun gives it its own session and captures its merged\n" +
			"stdout and stderr to the job's log, so nothing has to be wrapped in setsid,\n" +
			"nohup, or a redirect.\n\n" +
			"The exit status is recorded by the process that waits on the work, inside\n" +
			"the environment, so no sentinel token and no shell expansion sit between the\n" +
			"work and its result. The id defaults to the name; re-using the id of a job\n" +
			"that is still running is refused, while re-using a finished one replaces it.\n\n" +
			"--agent runs an AI tool instead of a command, with the trailing arguments as\n" +
			"the prompt. erun invokes the tool in its streaming mode, which is what makes\n" +
			"an agent run observable at all: left to its default the tool prints nothing\n" +
			"until it exits, so a multi-hour run reports no output while it is actively\n" +
			"editing files. status then reports the current activity, not only \"running\".",
		Example: "  # Start a test suite and come back for the result.\n" +
			"  erun job start --tenant team --environment dev --name suite -- ./gradlew test\n" +
			"  erun job await --tenant team --environment dev --id suite --timeout 5m\n\n" +
			"  # Start an agent and watch what it is doing.\n" +
			"  erun job start --tenant team --environment dev --name sweep --agent claude -- 'fix the failing tests'\n" +
			"  erun job status --tenant team --environment dev --id sweep",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := common.StartEnvironmentJobParams{
				Tenant:         tenant,
				Environment:    environment,
				Name:           name,
				ID:             id,
				Dir:            dir,
				MaxOutputBytes: maxOutputBytes,
				LeaseTTL:       leaseTTL,
			}
			if strings.TrimSpace(agent) != "" {
				params.Agent = agent
				params.Prompt = strings.Join(args, " ")
			} else {
				params.Command = args
			}
			return runJobStart(cmd, resolveOpen, params)
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "What the work is, shown wherever the environment reports as busy")
	cmd.Flags().StringVar(&id, "id", "", "Handle to address the job by (defaults to the name)")
	cmd.Flags().StringVar(&dir, "dir", "", "Working directory in the environment to run the command from")
	cmd.Flags().StringVar(&agent, "agent", "", "Run an AI tool ("+strings.Join(common.AgentJobTools, " or ")+") in streaming mode, taking the trailing arguments as its prompt")
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", common.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output; past it output is dropped and the job says so")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", common.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL the job renews inside while it runs")
	addDryRunFlag(cmd)
	return cmd
}

func runJobStart(cmd *cobra.Command, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	job, resolved, err := startJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	// A dry run resolved a plan and started nothing, so it says nothing about a
	// job having started; the trace above is the whole answer.
	if !resolved {
		return nil
	}
	if ctx.Output != common.OutputJSON {
		if _, err := fmt.Fprintf(ctx.Stdout, "job started: %s (id %s), output at %s\n", job.Name, job.ID, job.LogPath); err != nil {
			return err
		}
	}
	return ctx.WriteResult(job)
}

func startJob(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	if !environmentTargetsItself() {
		return startJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	// The CLI binary is the supervisor: an already-resolved absolute path, so a
	// job never depends on what the caller's PATH happens to hold.
	supervisor, err := erunExecutablePath()
	if err != nil {
		return common.EnvironmentJob{}, false, err
	}
	params.SupervisorPath = supervisor
	job, err := common.StartEnvironmentJob(commandCtx, params)
	if err != nil {
		return common.EnvironmentJob{}, false, err
	}
	return job, !commandCtx.DryRun, nil
}

func newJobAttachCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var pid int
	var logPath string
	var leaseTTL time.Duration
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Give work started another way a job handle and an activity lease",
		Long: "Use this for work erun did not start but that should still be visible and\n" +
			"protected from idle-stop. erun tracks the pid you name and nothing else, so\n" +
			"the job reads as running while that process lives and as unknown once it is\n" +
			"gone — it can never report an exit status, because nothing erun ran was\n" +
			"waiting on the process to observe one. Re-running attach with the same id\n" +
			"renews the lease.",
		Example: "  erun job attach --tenant team --environment dev --name overnight-index --pid 4242",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobAttach(cmd, resolveOpen, common.AttachEnvironmentJobParams{
				Tenant:      tenant,
				Environment: environment,
				Name:        name,
				ID:          id,
				PID:         pid,
				LogPath:     logPath,
				LeaseTTL:    leaseTTL,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "What the work is, shown wherever the environment reports as busy")
	cmd.Flags().StringVar(&id, "id", "", "Handle to address the job by (defaults to the name)")
	cmd.Flags().IntVar(&pid, "pid", 0, "Process in the environment to track; the job resolves against this pid and nothing else")
	cmd.Flags().StringVar(&logPath, "log", "", "File in the environment the work is already writing its output to, so job output can serve it")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", common.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL; re-attach to renew")
	addDryRunFlag(cmd)
	return cmd
}

func runJobAttach(cmd *cobra.Command, resolveOpen OpenResolver, params common.AttachEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	job, resolved, err := attachJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != common.OutputJSON {
		if _, err := fmt.Fprintf(ctx.Stdout, "job attached: %s (id %s), tracking pid %d\n", job.Name, job.ID, job.PID); err != nil {
			return err
		}
	}
	return ctx.WriteResult(job)
}

func attachJob(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.AttachEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	if !environmentTargetsItself() {
		return attachJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	job, err := common.AttachEnvironmentJob(commandCtx, params)
	if err != nil {
		return common.EnvironmentJob{}, false, err
	}
	return job, !commandCtx.DryRun, nil
}

func newJobStatusCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report one job's state and outcome, or every retained job",
		Long: "Answers running, exited, or unknown — never a partial answer. A job whose\n" +
			"supervisor is gone without an outcome reads as unknown rather than as\n" +
			"success, which is what makes it safe to act on. Finished jobs stay readable\n" +
			"for 24 hours so a caller that reconnects after the work ended can still\n" +
			"learn what happened.",
		Example: "  erun job status --tenant team --environment dev\n" +
			"  erun job status --tenant team --environment dev --id suite --output json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobStatus(cmd, resolveOpen, tenant, environment, id)
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job to report; omit for every retained job")
	addDryRunFlag(cmd)
	return cmd
}

func runJobStatus(cmd *cobra.Command, resolveOpen OpenResolver, tenant, environment, id string) error {
	if err := validateJobTarget(tenant, environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	single := strings.TrimSpace(id) != ""
	job, jobs, resolved, err := readJobStatus(cmd.Context(), ctx, resolveOpen, tenant, environment, id)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if single {
		if ctx.Output != common.OutputJSON {
			if _, err := fmt.Fprintln(ctx.Stdout, jobStatusLine(job)); err != nil {
				return err
			}
		}
		return ctx.WriteResult(job)
	}
	if ctx.Output != common.OutputJSON {
		if err := writeJobList(ctx, jobs); err != nil {
			return err
		}
	}
	return ctx.WriteResult(jobs)
}

func readJobStatus(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id string) (common.EnvironmentJob, []common.EnvironmentJob, bool, error) {
	if !environmentTargetsItself() {
		return readJobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	}
	return readStoredJobStatus(commandCtx, tenant, environment, id)
}

func readJobStatusFromEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id string) (common.EnvironmentJob, []common.EnvironmentJob, bool, error) {
	result, resolved, err := jobStatusFromEnvironment(ctx, commandCtx, resolveOpen, tenant, environment, id)
	if err != nil || !resolved {
		return common.EnvironmentJob{}, nil, resolved, err
	}
	jobs := result.Jobs
	if jobs == nil {
		jobs = []common.EnvironmentJob{}
	}
	if strings.TrimSpace(id) == "" {
		return common.EnvironmentJob{}, jobs, resolved, nil
	}
	if result.Job == nil {
		return common.EnvironmentJob{}, jobs, resolved, fmt.Errorf("%s/%s returned no job for id %q", tenant, environment, id)
	}
	return *result.Job, jobs, resolved, nil
}

func readStoredJobStatus(commandCtx common.Context, tenant, environment, id string) (common.EnvironmentJob, []common.EnvironmentJob, bool, error) {
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "status", tenant, environment, id)
		return common.EnvironmentJob{}, nil, false, nil
	}
	now := time.Now()
	if strings.TrimSpace(id) != "" {
		job, err := common.LoadEnvironmentJob(tenant, environment, id, now)
		if err != nil {
			return common.EnvironmentJob{}, nil, false, err
		}
		return job, []common.EnvironmentJob{job}, true, nil
	}
	jobs, err := common.LoadEnvironmentJobs(tenant, environment, now)
	if err != nil {
		return common.EnvironmentJob{}, nil, false, err
	}
	if jobs == nil {
		jobs = []common.EnvironmentJob{}
	}
	return common.EnvironmentJob{}, jobs, true, nil
}

func writeJobList(ctx common.Context, jobs []common.EnvironmentJob) error {
	if len(jobs) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no jobs")
		return err
	}
	for _, job := range jobs {
		if err := writeLabeledValue(ctx, job.ID, jobStatusLine(job)); err != nil {
			return err
		}
	}
	return nil
}

// jobStatusLine renders the outcome without ever implying one that was not
// recorded: an unknown job says so and says why.
func jobStatusLine(job common.EnvironmentJob) string {
	switch job.State {
	case common.EnvironmentJobStateRunning:
		line := fmt.Sprintf("running: %s", job.Name)
		if job.ChildPID > 0 {
			line += fmt.Sprintf(", pid %d", job.ChildPID)
		} else if job.PID > 0 {
			line += fmt.Sprintf(", pid %d", job.PID)
		}
		return line + jobAgentSuffix(job) + jobOutputSuffix(job)
	case common.EnvironmentJobStateExited:
		line := fmt.Sprintf("exited %d: %s", jobExitCodeOrUnset(job), job.Name)
		if strings.TrimSpace(job.Signal) != "" {
			line += fmt.Sprintf(" (signal %s)", job.Signal)
		}
		return line + jobAgentSuffix(job) + jobOutputSuffix(job)
	default:
		line := fmt.Sprintf("unknown: %s", job.Name)
		if strings.TrimSpace(job.Reason) != "" {
			line += " (" + job.Reason + ")"
		}
		return line + jobAgentSuffix(job) + jobOutputSuffix(job)
	}
}

// jobAgentSuffix reports what an agent run is doing right now. An agent job that
// has emitted nothing yet says so rather than reading as an idle one — the
// difference is exactly what a caller polling for progress needs.
func jobAgentSuffix(job common.EnvironmentJob) string {
	if job.Kind != common.EnvironmentJobKindAgent {
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

func jobOutputSuffix(job common.EnvironmentJob) string {
	suffix := fmt.Sprintf(", %d bytes of output", job.OutputBytes)
	if job.OutputTruncated {
		suffix += " (truncated at the output cap)"
	}
	return suffix
}

func jobExitCodeOrUnset(job common.EnvironmentJob) int {
	if job.ExitCode == nil {
		return -1
	}
	return *job.ExitCode
}

func newJobAwaitCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "await",
		Short: "Wait a bounded time for a job to finish, reporting a timeout distinctly",
		Long: "The wait is bounded and the call returns either an outcome or \"still\n" +
			"running\" — it never holds a connection open for the work's lifetime, which\n" +
			"is what drops under load and leaves a caller unable to tell a dead stream\n" +
			"from a dead job.\n\n" +
			"Exit codes are the contract: 0 when the job exited 0, 124 when the timeout\n" +
			"elapsed with the job still running, 125 when the outcome is unknown, and 1\n" +
			"when the job exited non-zero (its real code is in the reported result).",
		Example: "  erun job await --tenant team --environment dev --id suite --timeout 2m",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobAwait(cmd, resolveOpen, common.AwaitEnvironmentJobParams{
				Tenant:      tenant,
				Environment: environment,
				ID:          id,
				Timeout:     timeout,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job to wait for")
	cmd.Flags().DurationVar(&timeout, "timeout", common.DefaultEnvironmentJobAwaitTimeout, "How long to wait before reporting the job as still running")
	addDryRunFlag(cmd)
	return cmd
}

func runJobAwait(cmd *cobra.Command, resolveOpen OpenResolver, params common.AwaitEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	result, resolved, err := awaitJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != common.OutputJSON {
		if _, err := fmt.Fprintln(ctx.Stdout, jobAwaitLine(result)); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return jobAwaitExit(result)
}

func awaitJob(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.AwaitEnvironmentJobParams) (common.AwaitEnvironmentJobResult, bool, error) {
	if !environmentTargetsItself() {
		return awaitJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "await", params.Tenant, params.Environment, params.ID)
		return common.AwaitEnvironmentJobResult{}, false, nil
	}
	result, err := common.AwaitEnvironmentJob(params)
	if err != nil {
		return common.AwaitEnvironmentJobResult{}, false, err
	}
	return result, true, nil
}

func jobAwaitLine(result common.AwaitEnvironmentJobResult) string {
	if result.TimedOut {
		return fmt.Sprintf("still running after %ds: %s", result.TimeoutSeconds, result.Job.Name)
	}
	return jobStatusLine(result.Job)
}

// jobAwaitExit maps the wait onto the process exit code. Every outcome an
// orchestrator must distinguish gets its own code, so a shell caller can branch
// without reading anything back out of a payload.
func jobAwaitExit(result common.AwaitEnvironmentJobResult) error {
	switch {
	case result.TimedOut:
		return internal.WithExitCode(fmt.Errorf("job %q is still running after %ds", result.Job.ID, result.TimeoutSeconds), jobAwaitTimeoutExitCode)
	case result.Job.State == common.EnvironmentJobStateUnknown:
		return internal.WithExitCode(fmt.Errorf("job %q has no recorded outcome: %s", result.Job.ID, result.Job.Reason), jobAwaitUnknownExitCode)
	case result.Job.Succeeded():
		return nil
	default:
		return fmt.Errorf("job %q exited %d", result.Job.ID, jobExitCodeOrUnset(result.Job))
	}
}

func newJobOutputCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	var offset int64
	var maxBytes int64
	cmd := &cobra.Command{
		Use:   "output",
		Short: "Read a job's captured output, including while it is still running",
		Long: "Output is served from the job's log as it stands, so progress is visible long\n" +
			"before the work exits. Read a page at a time and pass the reported next\n" +
			"offset back to continue where you left off.",
		Example: "  erun job output --tenant team --environment dev --id suite --offset 4096",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobOutput(cmd, resolveOpen, common.ReadEnvironmentJobOutputParams{
				Tenant:      tenant,
				Environment: environment,
				ID:          id,
				Offset:      offset,
				MaxBytes:    maxBytes,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job whose output to read")
	cmd.Flags().Int64Var(&offset, "offset", 0, "Byte offset to read from, so a poll continues rather than repeats")
	cmd.Flags().Int64Var(&maxBytes, "max-bytes", common.DefaultEnvironmentJobOutputReadBytes, "Most bytes to return in this read")
	addDryRunFlag(cmd)
	return cmd
}

func runJobOutput(cmd *cobra.Command, resolveOpen OpenResolver, params common.ReadEnvironmentJobOutputParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	output, resolved, err := readJobOutput(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != common.OutputJSON {
		if _, err := fmt.Fprint(ctx.Stdout, output.Output); err != nil {
			return err
		}
		// The next offset is what makes a follow-up read incremental, and it is not
		// inferable from the bytes when the page ended mid-line.
		if _, err := fmt.Fprintf(ctx.Stderr, "next offset: %d (more: %t, complete: %t)\n", output.NextOffset, output.HasMore, output.Complete); err != nil {
			return err
		}
	}
	return ctx.WriteResult(output)
}

func readJobOutput(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.ReadEnvironmentJobOutputParams) (common.EnvironmentJobOutput, bool, error) {
	if !environmentTargetsItself() {
		return jobOutputFromEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "output", params.Tenant, params.Environment, params.ID)
		return common.EnvironmentJobOutput{}, false, nil
	}
	output, err := common.ReadEnvironmentJobOutput(params)
	if err != nil {
		return common.EnvironmentJobOutput{}, false, err
	}
	return output, true, nil
}

func newJobCancelCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var id string
	var signal string
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Signal a running job's work by its recorded process, never by pattern",
		Long: "The target comes from the job record, so a cancel can only ever reach the\n" +
			"work it names — not a process that merely looks like it, and not the shell\n" +
			"issuing the cancel. The job's supervisor is deliberately left alone so it\n" +
			"survives to record the outcome the cancel produced, which then reads back\n" +
			"as a normal exited job carrying the signal.",
		Example: "  erun job cancel --tenant team --environment dev --id suite\n" +
			"  erun job cancel --tenant team --environment dev --id suite --signal KILL",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobCancel(cmd, resolveOpen, common.CancelEnvironmentJobParams{
				Tenant:      tenant,
				Environment: environment,
				ID:          id,
				Signal:      signal,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job to cancel")
	cmd.Flags().StringVar(&signal, "signal", "TERM", "Signal to send: TERM, INT, HUP, or KILL")
	addDryRunFlag(cmd)
	return cmd
}

func runJobCancel(cmd *cobra.Command, resolveOpen OpenResolver, params common.CancelEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := commandContext(cmd)
	result, resolved, err := cancelJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != common.OutputJSON {
		message := fmt.Sprintf("job already finished: %s (%s)", result.Job.ID, result.Job.State)
		if result.Signalled {
			message = fmt.Sprintf("job cancelled: %s, SIG%s to process group %d", result.Job.ID, result.Signal, result.TargetPID)
		}
		if _, err := fmt.Fprintln(ctx.Stdout, message); err != nil {
			return err
		}
	}
	return ctx.WriteResult(result)
}

func cancelJob(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.CancelEnvironmentJobParams) (common.CancelEnvironmentJobResult, bool, error) {
	if !environmentTargetsItself() {
		return cancelJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	result, err := common.CancelEnvironmentJob(commandCtx, params)
	if err != nil {
		return common.CancelEnvironmentJobResult{}, false, err
	}
	return result, !commandCtx.DryRun, nil
}

// newJobSuperviseCmd is the process a started job actually runs as. It is the
// only writer of a job record, because it is the only thing waiting on the work
// and therefore the only thing that can observe an exit status first-hand. It
// always runs where the work runs, so it never reaches for an environment's edge.
// Hidden: nothing calls it but `job start`.
func newJobSuperviseCmd() *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var dir string
	var agent string
	var maxOutputBytes int64
	var leaseTTL time.Duration
	cmd := &cobra.Command{
		Use:    "supervise [flags] -- <command> [args...]",
		Short:  "Run a job's work, hold its lease, and record the outcome (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return common.RunEnvironmentJobSupervisor(common.EnvironmentJobSupervisorParams{
				Tenant:         tenant,
				Environment:    environment,
				ID:             id,
				Name:           name,
				Dir:            dir,
				Agent:          agent,
				Command:        args,
				MaxOutputBytes: maxOutputBytes,
				LeaseTTL:       leaseTTL,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "Job name")
	cmd.Flags().StringVar(&id, "id", "", "Job id")
	cmd.Flags().StringVar(&dir, "dir", "", "Working directory for the work")
	cmd.Flags().StringVar(&agent, "agent", "", "AI tool the command runs, so its event stream is folded into progress")
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", common.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", common.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL")
	return cmd
}

// erunExecutablePath resolves this binary's own path, which is what supervises
// a started job. Resolving it here rather than looking "erun" up on PATH means a
// job is supervised by the same build that started it.
func erunExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve the erun executable to supervise the job: %w", err)
	}
	return executable, nil
}

func addJobTargetFlags(cmd *cobra.Command, tenant, environment *string) {
	cmd.Flags().StringVar(tenant, "tenant", "", "Tenant whose environment the job belongs to")
	cmd.Flags().StringVar(environment, "environment", "", "Environment the job runs in")
}

func validateJobTarget(tenant, environment string) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	return nil
}
