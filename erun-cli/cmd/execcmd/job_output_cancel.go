package execcmd

import (
	"context"
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/spf13/cobra"
)

func newJobOutputCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
			return runJobOutput(cmd, resolveOpen, jobspkg.ReadEnvironmentJobOutputParams{
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
	cmd.Flags().Int64Var(&maxBytes, "max-bytes", jobspkg.DefaultEnvironmentJobOutputReadBytes, "Most bytes to return in this read")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runJobOutput(cmd *cobra.Command, resolveOpen shared.OpenResolver, params jobspkg.ReadEnvironmentJobOutputParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	output, resolved, err := readJobOutput(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != core.OutputJSON {
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

func readJobOutput(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobspkg.ReadEnvironmentJobOutputParams) (jobspkg.EnvironmentJobOutput, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return jobOutputFromEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	if commandCtx.DryRun {
		commandCtx.TraceCommand("", "job", "output", params.Tenant, params.Environment, params.ID)
		return jobspkg.EnvironmentJobOutput{}, false, nil
	}
	output, err := jobspkg.ReadEnvironmentJobOutput(params)
	if err != nil {
		return jobspkg.EnvironmentJobOutput{}, false, err
	}
	return output, true, nil
}

func newJobCancelCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
			return runJobCancel(cmd, resolveOpen, jobspkg.CancelEnvironmentJobParams{
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runJobCancel(cmd *cobra.Command, resolveOpen shared.OpenResolver, params jobspkg.CancelEnvironmentJobParams) error {
	if err := validateJobTarget(params.Tenant, params.Environment); err != nil {
		return err
	}
	ctx := shared.CommandContext(cmd)
	result, resolved, err := cancelJob(cmd.Context(), ctx, resolveOpen, params)
	if err != nil {
		return err
	}
	if !resolved {
		return nil
	}
	if ctx.Output != core.OutputJSON {
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

func cancelJob(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobspkg.CancelEnvironmentJobParams) (jobspkg.CancelEnvironmentJobResult, bool, error) {
	if !shared.EnvironmentTargetsItself() {
		return cancelJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	result, err := jobspkg.CancelEnvironmentJob(commandCtx, params)
	if err != nil {
		return jobspkg.CancelEnvironmentJobResult{}, false, err
	}
	return result, !commandCtx.DryRun, nil
}
