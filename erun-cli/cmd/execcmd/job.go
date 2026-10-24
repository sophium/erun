package execcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
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
func newJobCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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

// DeprecatedTopLevelJobCmd mounts the same verb tree at the top level for one
// release: `erun job <verb>` keeps working, with Cobra's own deprecation notice pointing callers
// at `erun exec job <verb>` before this alias is removed.
func DeprecatedTopLevelJobCmd(resolveOpen shared.OpenResolver) *cobra.Command {
	cmd := newJobCmd(resolveOpen)
	cmd.Deprecated = "use `erun exec job` instead; this command will be removed in a future release"
	return cmd
}

// jobStartLongHelp is `erun exec job start`'s own help body, kept beside the
// command rather than inside its constructor: it is the longest single piece
// of user-facing text in this file, and inlining it buried the command's
// actual wiring under it.
const jobStartLongHelp = "The command keeps running after this call returns and after the caller's\n" +
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
	"about work it is still waiting on -- there is no such notification, with one\n" +
	"narrow exception. If this job's own process ends while a job it started has\n" +
	"not reached a verdict, or has reached a bad one, erun resumes the same\n" +
	"session with the concrete outcome so the agent can act on it -- fix it,\n" +
	"verify it, or explain why it cannot be resolved. This is bounded (a small\n" +
	"fixed count and a wall-clock budget, both shown by `job status` as\n" +
	"\"resumed N/M\") and applies only to that one narrow case, never to a plain\n" +
	"failing exit code: past the bound, or for any other outcome, there is still\n" +
	"no notification and no further \"later\".\n\n" +
	"--env sets additional environment for the job's own process, on top of what\n" +
	"it inherits from the environment's runtime pod — for example raising\n" +
	"CLAUDE_CODE_MAX_OUTPUT_TOKENS for one agent run. Values land in the job\n" +
	"supervisor's argv, visible to anything that can list processes in this\n" +
	"environment, so this is not where secrets belong; PATH, LD_PRELOAD, and a\n" +
	"few other names that could redirect what the job executes are refused.\n\n" +
	"When this start itself runs from inside another job's own work (an agent\n" +
	"job running `job start` for a full build, the common case), that other job waits\n" +
	"for this one to reach a verdict before it reports its own outcome — its exit\n" +
	"code alone is never trusted while work it started has not finished. Pass\n" +
	"--handoff for work that is meant to outlive the caller on purpose (a release,\n" +
	"a long render): it excludes this job from that wait entirely.\n\n" +
	"--exclusive declares that the work needs the environment to itself. While it\n" +
	"runs, every other job start here is refused and told which job holds the\n" +
	"environment, rather than being queued or quietly allowed to contend. That\n" +
	"includes plain jobs, not only other --exclusive ones: a full build needs\n" +
	"protecting less from another one than from everything else beside it. Reach\n" +
	"for it whenever the work saturates the environment and a neighbour would make\n" +
	"its result meaningless -- a full check run measured 7 minutes alone and 17\n" +
	"minutes with a second batch beside it, going red on two tests that pass\n" +
	"standalone. The claim is a lease, not a lock: it expires without renewal and\n" +
	"is reclaimed once its holder's process is gone, so a crashed job cannot pin\n" +
	"the environment. Work this job itself starts is exempt -- a nested job runs\n" +
	"under this claim instead of being refused by it."

func newJobStartCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
	var exclusive bool
	cmd := &cobra.Command{
		Use:   "start [flags] -- <command> [args...]",
		Short: "Run a command, or an AI agent, as a detached job and return its handle",
		Long:  jobStartLongHelp,
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
			"  erun exec job start --tenant team --environment dev --name release --handoff -- erun release\n\n" +
			"  # Run a full build that must not share the environment with anything else.\n" +
			"  erun exec job start --tenant team --environment dev --name build --exclusive -- erun build",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envMap, err := parseJobEnvFlags(env)
			if err != nil {
				return err
			}
			params := jobspkg.StartEnvironmentJobParams{
				Tenant:         tenant,
				Environment:    environment,
				Name:           name,
				ID:             id,
				Dir:            dir,
				Env:            envMap,
				MaxOutputBytes: maxOutputBytes,
				LeaseTTL:       leaseTTL,
				Handoff:        handoff,
				Exclusive:      exclusive,
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
	cmd.Flags().StringVar(&agent, "agent", "", "Run an AI tool ("+strings.Join(jobspkg.AgentJobTools, " or ")+") in streaming mode, taking the trailing arguments as its prompt")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Additional KEY=VALUE environment for the job's process; repeat for several. Not for secrets: values are visible in the job supervisor's argv")
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", jobspkg.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output; past it output is dropped and the job says so")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", activity.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL the job renews inside while it runs")
	cmd.Flags().BoolVar(&handoff, "handoff", false, "Mark this job as deliberately meant to outlive whatever starts it, excluding it from that job's own finish check")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "Claim the environment for this job's lifetime; while it is held, every other job start here is refused and told which job holds it")
	shared.AddDryRunFlag(cmd)
	return cmd
}

// parseJobEnvFlags turns repeated --env KEY=VALUE flag values into a map. It
// only splits the flag's own shape; StartEnvironmentJobParams.normalize (and,
// server-side, the supervisor itself) is what actually validates each name.
func parseJobEnvFlags(pairs []string) (map[string]string, error) {
	var out map[string]string
	if len(pairs) > 0 {
		out = make(map[string]string, len(pairs))
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("--env %q must be in KEY=VALUE form", pair)
		}
		out[key] = value
	}
	return out, nil
}

func runJobStart(cmd *cobra.Command, resolveOpen shared.OpenResolver, params jobspkg.StartEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	job, resolved, err := startJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	// A dry run resolved a plan and started nothing, so it says nothing about a
	// job having started; the trace above is the whole answer.
	if !resolved {
		return nil
	}
	if ctx.Output != core.OutputJSON {
		if _, err := fmt.Fprintf(ctx.Stdout, "job started: %s (id %s), output at %s\n", job.Name, job.ID, job.LogPath); err != nil {
			return err
		}
	}
	return ctx.WriteResult(job)
}

func startJob(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobspkg.StartEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return startJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	// The CLI binary is the supervisor: an already-resolved absolute path, so a
	// job never depends on what the caller's PATH happens to hold.
	supervisor, err := shared.ErunExecutablePath()
	if err != nil {
		return sharedpkg.EnvironmentJob{}, false, err
	}
	params.SupervisorPath = supervisor
	job, err := jobspkg.StartEnvironmentJob(commandCtx, params)
	if err != nil {
		return sharedpkg.EnvironmentJob{}, false, err
	}
	return job, !commandCtx.DryRun, nil
}

func newJobAttachCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
			return runJobAttach(cmd, resolveOpen, jobspkg.AttachEnvironmentJobParams{
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
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", activity.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL; re-attach to renew")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runJobAttach(cmd *cobra.Command, resolveOpen shared.OpenResolver, params jobspkg.AttachEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	job, resolved, err := attachJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != core.OutputJSON {
		if _, err := fmt.Fprintf(ctx.Stdout, "job attached: %s (id %s), tracking pid %d\n", job.Name, job.ID, job.PID); err != nil {
			return err
		}
	}
	return ctx.WriteResult(job)
}

func attachJob(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobspkg.AttachEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return attachJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	job, err := jobspkg.AttachEnvironmentJob(commandCtx, params)
	if err != nil {
		return sharedpkg.EnvironmentJob{}, false, err
	}
	return job, !commandCtx.DryRun, nil
}

// newJobSuperviseCmd is the process a started job actually runs as. It is the
// only writer of a job record, because it is the only thing waiting on the work
// and therefore the only thing that can observe an exit status first-hand. It
// always runs where the work runs, so it never reaches for an environment's edge.
// Hidden: nothing invokes it directly. It is only ever re-exec'd by
// jobspkg.StartEnvironmentJob, which always re-execs it
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
	var exclusive bool
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
			return jobspkg.RunEnvironmentJobSupervisor(jobspkg.EnvironmentJobSupervisorParams{
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
				Exclusive:      exclusive,
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
	cmd.Flags().Int64Var(&maxOutputBytes, "max-output-bytes", jobspkg.DefaultEnvironmentJobOutputLimitBytes, "Cap on captured output")
	cmd.Flags().DurationVar(&leaseTTL, "lease-ttl", activity.DefaultEnvironmentActivityLeaseTTL, "Activity lease TTL")
	cmd.Flags().BoolVar(&handoff, "handoff", false, "Mark this job as deliberately meant to outlive whatever starts it")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "This job holds the environment's exclusivity claim, already taken by the start; renew it while the work runs and release it on the way out")
	cmd.Flags().StringVar(&startedByJobID, "started-by-job-id", "", "Explicit override for the job this work records as its parent, for a start forwarded through a channel (the MCP edge) that cannot carry ERUN_JOB_ID inheritance itself")
	return cmd
}

func addJobTargetFlags(cmd *cobra.Command, tenant, environment *string) {
	cmd.Flags().StringVar(tenant, "tenant", "", "Tenant whose environment the job belongs to")
	cmd.Flags().StringVar(environment, "environment", "", "Environment the job runs in")
}

func validateJobTarget(tenant, environment string) error {
	missing := shared.MissingTenantOrEnvironmentFlags(tenant, environment)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s not set: erun job commands run outside any resolved environment and never read the ambient config -- pass --tenant and --environment explicitly",
		strings.Join(missing, " and "),
	)
}
