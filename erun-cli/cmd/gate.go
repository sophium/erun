package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newGateCmd builds `erun gate`, the queue view an operator needs: what is
// being gated right now, what did recent gates decide, and (for a red one)
// which gate step failed and where to read it -- answerable without knowing
// any job ids and independent of whether the change gated is an erun review
// or a plain branch (e.g. a repository whose changes arrive as GitHub pull
// requests). `erun review queue list` remains the complementary view of
// what is *waiting* behind a review-driven merge; this does not duplicate
// it.
func newGateCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := newCommandGroup(
		"gate",
		"See what is being gated, and what recent gates decided",
		newGateListCmd(store, &alias, deps),
		newGateShowCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newGateListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.GateRunListParams
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List gate runs: what is gating now, and what recent gates decided",
		Long: "List gate runs, most recent first, narrowed by any combination of the filters below. Each entry " +
			"names the branch, the prospective merge commit actually tested, the target, and the verdict -- " +
			"and, for a failed one, which gate step failed and where to read it.\n\n" +
			"A RUNNING entry is being gated right now. INCONCLUSIVE means the gate never reached a real " +
			"verdict (a wrapper timeout, an environment fault) -- read it as unresolved, not as a failure.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun gate list --target-branch main\n  erun gate list --status FAILED",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			runs, err := common.RunGateRunList(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun gate list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeGateRunList(ctx, runs); err != nil {
					return err
				}
			}
			return ctx.WriteResult(runs)
		},
	}
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Filter by target branch")
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Filter by source branch")
	cmd.Flags().StringVar(&params.Status, "status", "", "Filter by status: RUNNING, PASSED, FAILED, or INCONCLUSIVE")
	addDryRunFlag(cmd)
	return cmd
}

func writeGateRunList(ctx common.Context, runs []common.PlatformGateRun) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no gate runs")
		return err
	}
	for _, run := range runs {
		if err := writeGateRunLine(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func writeGateRunLine(ctx common.Context, run common.PlatformGateRun) error {
	suffix := ""
	if run.Status == "FAILED" {
		suffix = fmt.Sprintf(" failing-step=%q", run.FailingStep)
		if run.LogRef != "" {
			suffix += fmt.Sprintf(" log-ref=%q", run.LogRef)
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s %s -> %s status=%s merge-commit=%s%s\n",
		run.GateRunID, run.SourceBranch, run.TargetBranch, run.Status, run.MergeCommit, suffix)
	return err
}

func newGateShowCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show GATE_RUN_ID",
		Short:        "Show one gate run in full",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			run, err := common.RunGateRunShow(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun gate show planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeGateRunLine(ctx, run); err != nil {
					return err
				}
			}
			return ctx.WriteResult(run)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}
