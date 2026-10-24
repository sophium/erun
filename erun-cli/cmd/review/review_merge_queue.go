package review

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/spf13/cobra"
)

func newReviewMergeQueueCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"queue",
		"Inspect and advance a target branch's merge queue",
		newReviewMergeQueueListCmd(store, alias, deps),
		newReviewMergeQueueAdvanceCmd(store, alias, deps),
		newReviewMergeQueueOverrideAdvanceCmd(store, alias, deps),
	)
}

func newReviewMergeQueueListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformMergeQueueParams
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a repository's merge queue for a target branch, in queue order",
		Long: "List the reviews waiting to merge into a target branch, in queue order.\n\n" +
			"A queue belongs to a repository, not to a target branch alone: two repositories your " +
			"tenant serves both have a main. --repository names it, defaulting to the current " +
			"checkout's origin — the same repository the merge-queue environment would promote " +
			"from. Every line names the review's own repository, so a queue that came back mixed " +
			"is visible rather than read as one repository's.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun review queue list --target-branch main\n" +
			"  erun review queue list --repository https://github.com/org/repo.git --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			reviews, err := reviewpkg.RunReviewMergeQueueList(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue list planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewList(ctx, reviews); err != nil {
					return err
				}
			}
			return ctx.WriteResult(reviews)
		},
	}
	cmd.Flags().StringVar(&params.Repository, "repository", "", "Repository whose queue to list (defaults to the current checkout's origin)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Target branch to list the merge queue for")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewMergeQueueAdvanceCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformMergeQueueParams
	cmd := &cobra.Command{
		Use:   "advance",
		Short: "Advance a repository's merge queue head to MERGE",
		Long: "Advance the next review waiting to merge into a target branch to MERGE, which starts " +
			"that review's merge-queue build.\n\n" +
			"A queue belongs to a repository, not to a target branch alone: --repository names it, " +
			"defaulting to the current checkout's origin, which is the repository this caller would " +
			"build. Advancing without one promotes across every repository your tenant serves.\n\n" +
			"A real, immediate mutation of shared control-plane state: it fails if the queue is " +
			"empty or its head is not READY, and refuses with the unresolved comment thread count " +
			"when the head still has open threads — resolve them first, or use " +
			"`erun review queue override-advance`.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun review queue advance --target-branch main\n" +
			"  erun review queue advance --repository https://github.com/org/repo.git --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			review, err := reviewpkg.RunReviewMergeQueueAdvance(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue advance planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	cmd.Flags().StringVar(&params.Repository, "repository", "", "Repository whose queue to advance (defaults to the current checkout's origin)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Target branch whose merge queue to advance")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewMergeQueueOverrideAdvanceCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformMergeQueueParams
	var reason string
	cmd := &cobra.Command{
		Use:   "override-advance",
		Short: "Bypass the unresolved-thread gate and advance the merge queue anyway",
		Long: "Bypass `erun review queue advance`'s unresolved-thread gate and advance a repository's " +
			"merge queue head to MERGE anyway.\n\n" +
			"--repository names the repository's queue, defaulting to the current checkout's origin, " +
			"exactly as `erun review queue advance` does. --reason is required and is recorded in the " +
			"platform's audit trail alongside the caller's identity — this is a deliberate, accountable " +
			"escape hatch, not a routine way to advance the queue. A real, immediate mutation of shared " +
			"control-plane state.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun review queue override-advance --target-branch main --reason \"hotfix, reviewers unavailable\"",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			review, err := reviewpkg.RunReviewMergeQueueOverrideAdvance(ctx, store, *alias, params, reason, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue override-advance planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	cmd.Flags().StringVar(&params.Repository, "repository", "", "Repository whose queue to advance (defaults to the current checkout's origin)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Target branch whose merge queue to advance")
	cmd.Flags().StringVar(&reason, "reason", "", "Why the unresolved-thread gate is being bypassed (required)")
	shared.AddDryRunFlag(cmd)
	return cmd
}
