package cmd

import (
	"fmt"
	"io"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// newReviewCmd builds `erun review`, the CLI's client for the erun platform's
// collaboration API (erun-backend-api's reviews/comments/builds/merge-queue
// routes), authenticating with the `erun`-type cloud alias `erun cloud init
// erun` / `erun cloud login` set up. It is the client the collaboration
// surface has always been missing (#1199): before this, the only way to
// start a review, comment on one, or advance the merge queue from a terminal
// or an agent was a hand-written HTTP request.
func newReviewCmd(store common.CloudReadStore, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := newCommandGroup(
		"review",
		"Review code changes on the erun platform",
		newReviewListCmd(store, &alias, deps),
		newReviewShowCmd(store, &alias, deps),
		newReviewCreateCmd(store, &alias, deps),
		newReviewCommentCmd(store, &alias, deps),
		newReviewCloseCmd(store, &alias, deps),
		newReviewMergeQueueCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newReviewListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.ReviewListParams
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reviews on the erun platform",
		Long: "List reviews on the erun platform, narrowed by any combination of the filters below.\n\n" +
			"--mine resolves to reviews you authored; --waiting-on-me resolves to reviews you are a " +
			"reviewer on. Both resolve your user id via a whoami call first and cannot be combined " +
			"with the equivalent explicit --author-user-id/--reviewer-user-id flag.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun review list --mine\n" +
			"  erun review list --waiting-on-me --status OPEN\n" +
			"  erun review list --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			reviews, err := common.RunReviewList(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewList(ctx, reviews); err != nil {
					return err
				}
			}
			return ctx.WriteResult(reviews)
		},
	}
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Filter by target branch")
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Filter by source branch")
	cmd.Flags().StringVar(&params.Status, "status", "", "Filter by status: OPEN, CLOSED, FAILED, READY, MERGE, or MERGED")
	cmd.Flags().StringVar(&params.AuthorUserID, "author-user-id", "", "Filter by author user id")
	cmd.Flags().StringVar(&params.ReviewerUserID, "reviewer-user-id", "", "Filter by reviewer user id")
	cmd.Flags().BoolVar(&params.Mine, "mine", false, "Show only reviews you authored")
	cmd.Flags().BoolVar(&params.WaitingOnMe, "waiting-on-me", false, "Show only reviews you are a reviewer on")
	addDryRunFlag(cmd)
	return cmd
}

func writeReviewList(ctx common.Context, reviews []common.PlatformReview) error {
	if len(reviews) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no reviews")
		return err
	}
	for _, review := range reviews {
		if err := writeReviewLine(ctx, review); err != nil {
			return err
		}
	}
	return nil
}

func writeReviewLine(ctx common.Context, review common.PlatformReview) error {
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) %s -> %s status=%s\n",
		review.Name, review.ReviewID, review.SourceBranch, review.TargetBranch, review.Status)
	return err
}

func newReviewShowCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show REVIEW_ID",
		Short:        "Show a review, its comment threads, and its recorded builds",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			detail, err := common.RunReviewShow(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review show planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewDetail(ctx, detail); err != nil {
					return err
				}
			}
			return ctx.WriteResult(detail)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func writeReviewDetail(ctx common.Context, detail common.ReviewDetail) error {
	if err := writeReviewLine(ctx, detail.Review); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "  builds: %d, comments: %d\n", len(detail.Builds), len(detail.Comments)); err != nil {
		return err
	}
	for _, comment := range detail.Comments {
		if err := writeReviewCommentLine(ctx, comment); err != nil {
			return err
		}
	}
	return nil
}

func writeReviewCommentLine(ctx common.Context, comment common.PlatformComment) error {
	prefix := "  "
	if strings.TrimSpace(comment.ParentCommentID) != "" {
		prefix = "    ↳ "
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%s[%s] %s:%d %s\n", prefix, comment.CommentID, comment.FilePath, comment.Line, comment.Body)
	return err
}

func newReviewCreateCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var params common.PlatformCreateReviewParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Open a review on the erun platform",
		Long: "Open a review on the erun platform.\n\n" +
			"--name is the eventual squash-merge message and must be unique per tenant; a colliding " +
			"name fails with a conflict. --source-branch must already exist on the remote — push it " +
			"first with `erun exec push` — since the review references it by name and the platform " +
			"can only ever fetch what has actually landed there. A real, immediate write, not a preview.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun exec push feature/add-widget\n  erun review create --name \"Add widget\" --source-branch feature/add-widget --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			review, err := common.RunReviewCreate(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review creation planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Review name (unique per tenant; the eventual squash-merge message)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Branch this review proposes merging into")
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Branch this review proposes merging (must already be pushed)")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewCommentCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var (
		commitID string
		filePath string
		line     int
		replyTo  string
	)
	cmd := &cobra.Command{
		Use:   "comment REVIEW_ID",
		Short: "Comment on a line of a review, or reply to an existing comment",
		Long: "Comment on a line of a review, or reply to an existing comment with --reply-to.\n\n" +
			"The comment body is read verbatim from stdin — never a shell, so nothing in it is " +
			"reinterpreted, mirroring `erun exec commit`'s own message input. A real, immediate write.",
		Example: "  echo 'nit: rename this' | erun review comment 018f... --commit abc123 --file main.go --line 42\n" +
			"  echo 'good catch, fixed' | erun review comment 018f... --commit abc123 --file main.go --line 42 --reply-to 018g...",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			body, err := io.ReadAll(ctx.Stdin)
			if err != nil {
				return fmt.Errorf("read comment body from stdin: %w", err)
			}
			comment, err := common.RunReviewComment(ctx, store, *alias, common.ReviewCommentParams{
				ReviewID:        args[0],
				CommitID:        commitID,
				FilePath:        filePath,
				Line:            line,
				Body:            string(body),
				ParentCommentID: replyTo,
			}, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review comment planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewCommentLine(ctx, comment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(comment)
		},
	}
	cmd.Flags().StringVar(&commitID, "commit", "", "Commit hash the comment is anchored to")
	cmd.Flags().StringVar(&filePath, "file", "", "File path the comment is anchored to")
	cmd.Flags().IntVar(&line, "line", 0, "Line number the comment is anchored to")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "Comment id to reply to, making this a reply in that thread")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewCloseCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "close REVIEW_ID",
		Short:        "Close a review without merging it",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			review, err := common.RunReviewClose(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review close planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func newReviewMergeQueueCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"queue",
		"Inspect and advance a target branch's merge queue",
		newReviewMergeQueueListCmd(store, alias, deps),
		newReviewMergeQueueAdvanceCmd(store, alias, deps),
	)
}

func newReviewMergeQueueListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var targetBranch string
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List a target branch's merge queue, in queue order",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun review queue list --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			reviews, err := common.RunReviewMergeQueueList(ctx, store, *alias, targetBranch, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewList(ctx, reviews); err != nil {
					return err
				}
			}
			return ctx.WriteResult(reviews)
		},
	}
	cmd.Flags().StringVar(&targetBranch, "target-branch", "", "Target branch to list the merge queue for")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewMergeQueueAdvanceCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var targetBranch string
	cmd := &cobra.Command{
		Use:   "advance",
		Short: "Advance a target branch's merge queue head to MERGED",
		Long: "Advance a target branch's merge queue head to MERGED.\n\n" +
			"A real, immediate mutation of shared control-plane state: it fails if the queue is " +
			"empty or its head is not READY. Until #1196's merge queue executor lands, MERGED is a " +
			"status only — nothing yet performs the actual git merge.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun review queue advance --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			review, err := common.RunReviewMergeQueueAdvance(ctx, store, *alias, targetBranch, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue advance planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	cmd.Flags().StringVar(&targetBranch, "target-branch", "", "Target branch whose merge queue to advance")
	addDryRunFlag(cmd)
	return cmd
}
