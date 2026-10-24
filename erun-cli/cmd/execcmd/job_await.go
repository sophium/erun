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
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

func newJobAwaitCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
			"just at a larger size. A job that runs longer than 10m (a full build with\n" +
			"its test suite routinely does) is not a case this command refuses to cover -- call it\n" +
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
			"  # A build that can run longer than the 10m ceiling: keep awaiting at the\n" +
			"  # ceiling until it stops reporting 124.\n" +
			"  erun exec job await --tenant team --environment dev --id suite --timeout 10m",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobAwait(cmd, resolveOpen, jobspkg.AwaitEnvironmentJobParams{
				Tenant:      tenant,
				Environment: environment,
				ID:          id,
				Timeout:     timeout,
			})
		},
	}
	addJobTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&id, "id", "", "Job to wait for")
	cmd.Flags().DurationVar(&timeout, "timeout", jobspkg.DefaultEnvironmentJobAwaitTimeout, fmt.Sprintf("How long to wait before reporting the job as still running (capped at %s; call await again to keep waiting past it)", jobspkg.MaxEnvironmentJobAwaitTimeout))
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runJobAwait(cmd *cobra.Command, resolveOpen shared.OpenResolver, params jobspkg.AwaitEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	result, resolved, err := awaitJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != core.OutputJSON {
		if _, err := fmt.Fprintln(ctx.Stdout, jobAwaitLine(result)); err != nil {
			return err
		}
	}
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	return jobAwaitExit(result)
}

func awaitJob(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobspkg.AwaitEnvironmentJobParams) (jobspkg.AwaitEnvironmentJobResult, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return awaitJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "await", params.Tenant, params.Environment, params.ID)
		return jobspkg.AwaitEnvironmentJobResult{}, false, nil
	}
	result, err := jobspkg.AwaitEnvironmentJob(params)
	if err != nil {
		return jobspkg.AwaitEnvironmentJobResult{}, false, err
	}
	return result, true, nil
}

func jobAwaitLine(result jobspkg.AwaitEnvironmentJobResult) string {
	if result.TimedOut {
		return fmt.Sprintf("still running after %ds: %s", result.TimeoutSeconds, result.Job.Name) + jobAliveSuffix(result.Job)
	}
	return jobStatusLine(result.Job)
}

// jobAwaitExit gives each outcome its own exit code so shell callers can branch.
func jobAwaitExit(result jobspkg.AwaitEnvironmentJobResult) error {
	switch {
	case result.TimedOut:
		return internal.WithExitCode(fmt.Errorf("job %q is still running after %ds", result.Job.ID, result.TimeoutSeconds), jobAwaitTimeoutExitCode)
	case result.Job.State == sharedpkg.EnvironmentJobStateUnknown:
		return internal.WithExitCode(fmt.Errorf("job %q has no recorded outcome: %s", result.Job.ID, result.Job.Reason), jobAwaitUnknownExitCode)
	case result.Job.Succeeded:
		return nil
	case result.Job.State == sharedpkg.EnvironmentJobStateAbandoned:
		return fmt.Errorf("job %q abandoned background work: %s", result.Job.ID, result.Job.Reason)
	case result.Job.State == sharedpkg.EnvironmentJobStateGateIncomplete:
		return fmt.Errorf("job %q ended while work it started was still running: %s", result.Job.ID, result.Job.Reason)
	case result.Job.WorktreeDirty:
		return fmt.Errorf("job %q ended with an uncommitted working tree: %s", result.Job.ID, jobWorktreeSummary(result.Job))
	case strings.TrimSpace(result.Job.StartedJobFailed) != "":
		return fmt.Errorf("job %q: %s", result.Job.ID, result.Job.StartedJobFailed)
	default:
		return jobFailedExitError(result.Job)
	}
}

// jobFailedExitError carries the signal, else the recorded reason.
func jobFailedExitError(job sharedpkg.EnvironmentJob) error {
	switch {
	case strings.TrimSpace(job.Signal) != "":
		return fmt.Errorf("job %q exited %d (signal %s)", job.ID, jobExitCodeOrUnset(job), job.Signal)
	case strings.TrimSpace(job.Reason) != "":
		return fmt.Errorf("job %q exited %d: %s", job.ID, jobExitCodeOrUnset(job), job.Reason)
	default:
		return fmt.Errorf("job %q exited %d", job.ID, jobExitCodeOrUnset(job))
	}
}
