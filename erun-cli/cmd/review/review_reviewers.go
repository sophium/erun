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

func newReviewReviewersCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"reviewers",
		"Assign and remove reviewers on a review",
		newReviewReviewersListCmd(store, alias, deps),
		newReviewReviewersAddCmd(store, alias, deps),
		newReviewReviewersRemoveCmd(store, alias, deps),
	)
}

func writeReviewerLine(ctx core.Context, reviewer platformclient.PlatformReviewer) error {
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s\n", reviewer.UserID)
	return err
}

func newReviewReviewersListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list REVIEW_ID",
		Short:        "List a review's assigned reviewers",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review reviewers list 018f...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			reviewers, err := reviewpkg.RunReviewReviewersList(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers list planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if len(reviewers) == 0 {
					if _, err := fmt.Fprintln(ctx.Stdout, "no reviewers"); err != nil {
						return err
					}
				}
				for _, reviewer := range reviewers {
					if err := writeReviewerLine(ctx, reviewer); err != nil {
						return err
					}
				}
			}
			return ctx.WriteResult(reviewers)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewReviewersAddCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var userID string
	cmd := &cobra.Command{
		Use:   "add REVIEW_ID",
		Short: "Assign a reviewer to a review",
		Long: "Assign a reviewer to a review.\n\n" +
			"--user-id must already be enrolled in your own tenant — checked before the network call, " +
			"not only by the platform's own tenant-scoped refusal. A real, immediate write; assigning " +
			"a reviewer gates no status transition (see `erun review queue`'s unresolved-thread gate " +
			"for what actually blocks a merge).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review reviewers add 018f... --user-id 018g...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			reviewer, err := reviewpkg.RunReviewReviewerAdd(ctx, store, *alias, args[0], userID, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers add planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewerLine(ctx, reviewer); err != nil {
					return err
				}
			}
			return ctx.WriteResult(reviewer)
		},
	}
	cmd.Flags().StringVar(&userID, "user-id", "", "User id to assign as a reviewer (required)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewReviewersRemoveCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var userID string
	cmd := &cobra.Command{
		Use:          "remove REVIEW_ID",
		Short:        "Remove a reviewer from a review",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review reviewers remove 018f... --user-id 018g...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			err := reviewpkg.RunReviewReviewerRemove(ctx, store, *alias, args[0], userID, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers remove planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "removed reviewer %s\n", userID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(map[string]string{"reviewId": args[0], "userId": userID})
		},
	}
	cmd.Flags().StringVar(&userID, "user-id", "", "User id to remove as a reviewer (required)")
	shared.AddDryRunFlag(cmd)
	return cmd
}
