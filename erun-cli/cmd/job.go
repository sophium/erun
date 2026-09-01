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

// newJobCmd builds the job verb tree, shared by its canonical mount point
// (`erun exec job`, alongside newJobSuperviseCmd) and its deprecated
// top-level alias (`erun job`, kept for one release). supervise is not
// included here: it is Hidden and only ever re-exec'd by StartEnvironmentJob
// at the argv erun-common's environmentJobSupervisorArgs builds, which always
// names the canonical path -- so the alias mount never needs its own copy.
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
	)
	return cmd
}

// deprecatedTopLevelJobCmd mounts the same verb tree at the top level for one
// release (#1246, following #1186's `job` -> `exec job` move): `erun job
// <verb>` keeps working, with Cobra's own deprecation notice pointing callers
// at `erun exec job <verb>` before this alias is removed.
func deprecatedTopLevelJobCmd(resolveOpen OpenResolver) *cobra.Command {
	cmd := newJobCmd(resolveOpen)
	cmd.Deprecated = "use `erun exec job` instead; this command will be removed in a future release"
	return cmd
}

func newJobStartCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var dir string
	var agent string
	var env []string
	var maxOutputBytes int64
	var leaseTTL time.Duration
	var handoff bool
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
			"editing files. status then reports the current activity, not only \"running\".\n" +
			"This is a one-shot, non-interactive run: nothing wakes it once it exits, so\n" +
			"the prompt must not end its own turn believing something will notify it\n" +
			"about work it is still waiting on -- there is no such notification.\n\n" +
			"--env sets additional environment for the job's own process, on top of what\n" +
			"it inherits from the environment's runtime pod — for example raising\n" +
			"CLAUDE_CODE_MAX_OUTPUT_TOKENS for one agent run. Values land in the job\n" +
			"supervisor's argv, visible to anything that can list processes in this\n" +
			"environment, so this is not where secrets belong; PATH, LD_PRELOAD, and a\n" +
			"few other names that could redirect what the job executes are refused.\n\n" +
			"When this start itself runs from inside another job's own work (an agent\n" +
			"job running `job start` for a gate, the common case), that other job waits\n" +
			"for this one to reach a verdict before it reports its own outcome — its exit\n" +
			"code alone is never trusted while work it started has not finished. Pass\n" +
			"--handoff for work that is meant to outlive the caller on purpose (a release,\n" +
			"a long render): it excludes this job from that wait entirely.",
		Example: "  # Start a test suite and come back for the result.\n" +
			"  erun exec job start --tenant team --environment dev --name suite -- ./gradlew test\n" +
			"  erun exec job await --tenant team --environment dev --id suite --timeout 5m\n\n" +
			"  # Start an agent and watch what it is doing.\n" +
			"  erun exec job start --tenant team --environment dev --name sweep --agent claude -- 'fix the failing tests'\n" +
			"  erun exec job status --tenant team --environment dev --id sweep\n\n" +
			"  # Raise an agent's output-token cap for this run only.\n" +
			"  erun exec job start --tenant team --environment dev --name sweep --agent claude \\\n" +
			"    --env CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000 -- 'rewrite the module'\n\n" +
			"  # Kick off a release that is meant to keep running past this run's own turn.\n" +
			"  erun exec job start --tenant team --environment dev --name release --handoff -- erun release",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envMap, err := parseJobEnvFlags(env)
			if err != nil {
				return err
			}
			params := common.StartEnvironmentJobParams{
				Tenant:         tenant,
				Environment:    environment,
				Name:           name,
				ID:             id,
				Dir:            dir,
				Env:            envMap,
				MaxOutputBytes: maxOutputBytes,
				LeaseTTL:       leaseTTL,
				Handoff:        handoff,
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
	cmd.Flags().StringArrayVar(&env, "env", nil, "Additional KEY=VALUE environment for the job's process; repeat for several. Not for secrets: values are visible in the job supervisor's argv")
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", common.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output; past it output is dropped and the job says so")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", common.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL the job renews inside while it runs")
	cmd.Flags().BoolVar(&handoff, "handoff", false, "Mark this job as deliberately meant to outlive whatever starts it, excluding it from that job's own finish check")
	addDryRunFlag(cmd)
	return cmd
}

// parseJobEnvFlags turns repeated --env KEY=VALUE flag values into a map. It
// only splits the flag's own shape; StartEnvironmentJobParams.normalize (and,
// server-side, the supervisor itself) is what actually validates each name.
func parseJobEnvFlags(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("--env %q must be in KEY=VALUE form", pair)
		}
		out[key] = value
	}
	return out, nil
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
		Example: "  erun exec job attach --tenant team --environment dev --name overnight-index --pid 4242",
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
		return jobRunningLine(job)
	case common.EnvironmentJobStateExited:
		return jobExitedLine(job)
	case common.EnvironmentJobStateAbandoned:
		return jobAbandonedLine(job)
	case common.EnvironmentJobStateGateIncomplete:
		return jobGateIncompleteLine(job)
	default:
		return jobUnknownLine(job)
	}
}

func jobRunningLine(job common.EnvironmentJob) string {
	line := fmt.Sprintf("running: %s", job.Name)
	if job.ChildPID > 0 {
		line += fmt.Sprintf(", pid %d", job.ChildPID)
	} else if job.PID > 0 {
		line += fmt.Sprintf(", pid %d", job.PID)
	}
	return line + jobAliveSuffix(job) + jobAgentSuffix(job) + jobOutputSuffix(job)
}

func jobExitedLine(job common.EnvironmentJob) string {
	line := fmt.Sprintf("exited %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	return line + jobAgentSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobCloneSuffix(job) + jobOutputSuffix(job)
}

// jobStartedJobFailedSuffix surfaces a job this job started and waited for
// (see EnvironmentJobStateGateIncomplete) that finished without succeeding --
// the one thing this job's own exit code cannot say once that wait is over.
func jobStartedJobFailedSuffix(job common.EnvironmentJob) string {
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
func jobCloneSuffix(job common.EnvironmentJob) string {
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
func jobWorktreeSuffix(job common.EnvironmentJob) string {
	if !job.WorktreeDirty {
		return ""
	}
	return ", " + jobWorktreeSummary(job)
}

// jobWorktreeSummary is the human-readable half of jobWorktreeSuffix, reused
// by jobAwaitExit's error text so the two surfaces never describe the same
// outcome differently.
func jobWorktreeSummary(job common.EnvironmentJob) string {
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
func jobAbandonedLine(job common.EnvironmentJob) string {
	line := fmt.Sprintf("abandoned %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobStartedJobFailedSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

// jobGateIncompleteLine is rendered distinctly from abandoned: the still-running
// work is a sibling job record this job started, not a process left behind in
// its own process group.
func jobGateIncompleteLine(job common.EnvironmentJob) string {
	line := fmt.Sprintf("gate-incomplete %d: %s", jobExitCodeOrUnset(job), job.Name)
	if strings.TrimSpace(job.Signal) != "" {
		line += fmt.Sprintf(" (signal %s)", job.Signal)
	}
	if strings.TrimSpace(job.Reason) != "" {
		line += " (" + job.Reason + ")"
	}
	return line + jobAgentSuffix(job) + jobWorktreeSuffix(job) + jobOutputSuffix(job)
}

func jobUnknownLine(job common.EnvironmentJob) string {
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
func jobAliveSuffix(job common.EnvironmentJob) string {
	if job.AliveAgeMs == nil {
		return ""
	}
	return fmt.Sprintf(", last beat %dms ago", *job.AliveAgeMs)
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
			"from a dead job. A job whose supervisor died reads back as unknown with\n" +
			"timedOut false, its own third case distinct from both success and \"still\n" +
			"running\" — never re-await it expecting a different answer.\n\n" +
			"The reported job's aliveAgeMs is the faster of the two signals: it crosses\n" +
			"the 5000ms caller threshold before state necessarily catches up, so a\n" +
			"caller in a hurry can act on it directly instead of waiting for the next\n" +
			"reconcile (see `erun exec job status --help`).\n\n" +
			"--timeout is capped at 10m: a wait bounded well past the length of a single\n" +
			"call is the same held-connection failure mode this command exists to avoid,\n" +
			"just at a larger size. A job that runs longer than 10m (the full test suite\n" +
			"gate routinely does) is not a case this command refuses to cover -- call it\n" +
			"again each time it reports the job still running (exit 124) rather than\n" +
			"asking for a single longer wait; a timeout past the cap is refused outright,\n" +
			"naming the 10m ceiling in the refusal.\n\n" +
			"Exit codes are the contract: 0 when the job exited 0, 124 when the timeout\n" +
			"elapsed with the job still running, 125 when the outcome is unknown, and 1\n" +
			"when the job exited non-zero (its real code is in the reported result). 124\n" +
			"is never a verdict on the job -- it means only that this one call's own\n" +
			"bounded wait elapsed, and the documented response is to call await again,\n" +
			"not to treat the silence as an answer.\n\n" +
			"An agent job's own process exiting is not this command's cue to stop\n" +
			"either: if that process started another job through its own Bash tool and\n" +
			"left it running, this job keeps reporting 124 (not 0) until that started\n" +
			"job reaches a verdict too. Keep awaiting this job's own id -- never trust\n" +
			"the underlying tool's transcript ending as the answer, since nothing wakes\n" +
			"a one-shot run back up to report one.",
		Example: "  erun exec job await --tenant team --environment dev --id suite --timeout 2m\n\n" +
			"  # A gate that can run longer than the 10m ceiling: keep awaiting at the\n" +
			"  # ceiling until it stops reporting 124.\n" +
			"  erun exec job await --tenant team --environment dev --id suite --timeout 10m",
		Args: cobra.NoArgs,
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
	cmd.Flags().DurationVar(&timeout, "timeout", common.DefaultEnvironmentJobAwaitTimeout, fmt.Sprintf("How long to wait before reporting the job as still running (capped at %s; call await again to keep waiting past it)", common.MaxEnvironmentJobAwaitTimeout))
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
		return fmt.Sprintf("still running after %ds: %s", result.TimeoutSeconds, result.Job.Name) + jobAliveSuffix(result.Job)
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
	case result.Job.Succeeded:
		return nil
	case result.Job.State == common.EnvironmentJobStateAbandoned:
		return fmt.Errorf("job %q abandoned background work: %s", result.Job.ID, result.Job.Reason)
	case result.Job.State == common.EnvironmentJobStateGateIncomplete:
		return fmt.Errorf("job %q ended while work it started was still running: %s", result.Job.ID, result.Job.Reason)
	case result.Job.WorktreeDirty:
		return fmt.Errorf("job %q ended with an uncommitted working tree: %s", result.Job.ID, jobWorktreeSummary(result.Job))
	case strings.TrimSpace(result.Job.StartedJobFailed) != "":
		return fmt.Errorf("job %q: %s", result.Job.ID, result.Job.StartedJobFailed)
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
			"offset back to continue where you left off. A silent-but-healthy command\n" +
			"(an image pull, a slow test) never advances outputBytes for minutes at a\n" +
			"time, so use the reported job's aliveAgeMs (see `erun exec job status --help`),\n" +
			"not output growth, to tell quiet-but-alive apart from actually dead.",
		Example: "  erun exec job output --tenant team --environment dev --id suite --offset 4096",
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
		Example: "  erun exec job cancel --tenant team --environment dev --id suite\n" +
			"  erun exec job cancel --tenant team --environment dev --id suite --signal KILL",
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
// Hidden: nothing invokes it directly. It is only ever re-exec'd by
// StartEnvironmentJob (erun-common/job_supervisor.go), which always re-execs it
// at `exec job supervise` regardless of which entry point started the job.
func newJobSuperviseCmd() *cobra.Command {
	var tenant string
	var environment string
	var name string
	var id string
	var dir string
	var agent string
	var env []string
	var maxOutputBytes int64
	var leaseTTL time.Duration
	var handoff bool
	var startedByJobID string
	cmd := &cobra.Command{
		Use:    "supervise [flags] -- <command> [args...]",
		Short:  "Run a job's work, hold its lease, and record the outcome (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envMap, err := parseJobEnvFlags(env)
			if err != nil {
				return err
			}
			return common.RunEnvironmentJobSupervisor(common.EnvironmentJobSupervisorParams{
				Tenant:         tenant,
				Environment:    environment,
				ID:             id,
				Name:           name,
				Dir:            dir,
				Agent:          agent,
				Command:        args,
				Env:            envMap,
				MaxOutputBytes: maxOutputBytes,
				LeaseTTL:       leaseTTL,
				Handoff:        handoff,
				StartedByJobID: startedByJobID,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&name, "name", "", "Job name")
	cmd.Flags().StringVar(&id, "id", "", "Job id")
	cmd.Flags().StringVar(&dir, "dir", "", "Working directory for the work")
	cmd.Flags().StringVar(&agent, "agent", "", "AI tool the command runs, so its event stream is folded into progress")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Additional KEY=VALUE environment for the job's process; repeat for several")
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", common.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", common.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL")
	cmd.Flags().BoolVar(&handoff, "handoff", false, "Mark this job as deliberately meant to outlive whatever starts it")
	cmd.Flags().StringVar(&startedByJobID, "started-by-job-id", "", "Explicit override for the job this work records as its parent, for a start forwarded through a channel (the MCP edge) that cannot carry ERUN_JOB_ID inheritance itself")
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
	missing := missingTenantOrEnvironmentFlags(tenant, environment)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s not set: erun job commands run outside any resolved environment and never read the ambient config -- pass --tenant and --environment explicitly",
		strings.Join(missing, " and "),
	)
}
