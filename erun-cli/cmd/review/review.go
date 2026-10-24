package review

import (
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/spf13/cobra"
)

// NewReviewCmd builds `erun review`, the CLI's client for the erun platform's
// collaboration API (erun-backend-api's reviews/comments/builds/merge-queue
// routes), authenticating with the `erun`-type cloud alias `erun cloud init
// erun` / `erun cloud login` set up.
func NewReviewCmd(store config.CloudReadStore, deps cloud.CloudDependencies) *cobra.Command {
	var alias string
	cmd := shared.NewCommandGroup(
		"review",
		"Review code changes on the erun platform",
		newReviewListCmd(store, &alias, deps),
		newReviewShowCmd(store, &alias, deps),
		newReviewCreateCmd(store, &alias, deps),
		newReviewCommentCmd(store, &alias, deps),
		newReviewResolveCmd(store, &alias, deps),
		newReviewUnresolveCmd(store, &alias, deps),
		newReviewCloseCmd(store, &alias, deps),
		newReviewRecordBuildCmd(store, &alias, deps),
		newReviewReportMergedCmd(store, &alias, deps),
		newReviewRequeueCmd(store, &alias, deps),
		newReviewReviewersCmd(store, &alias, deps),
		newReviewMergeQueueCmd(store, &alias, deps),
	)
	cmd.PersistentFlags().StringVar(&alias, "erun-alias", "", "erun platform cloud alias to target (defaults to the sole configured erun-type alias)")
	return cmd
}

func newReviewListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params reviewpkg.ReviewListParams
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reviews on the erun platform",
		Long: "List reviews on the erun platform, narrowed by any combination of the filters below.\n\n" +
			"--mine resolves to reviews you authored; --waiting-on-me resolves to reviews you are a " +
			"reviewer on. Both resolve your user id via a whoami call first and cannot be combined " +
			"with the equivalent explicit --author-user-id/--reviewer-user-id flag.\n\n" +
			"--repository narrows the listing to one repository, which is what separates two " +
			"repositories your tenant serves that propose the same branch pair. It is not defaulted " +
			"from your checkout: a listing is how you find work across every repository. Every line " +
			"names the review's own repository so a mixed listing is legible.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun review list --mine\n" +
			"  erun review list --waiting-on-me --status OPEN\n" +
			"  erun review list --target-branch main\n" +
			"  erun review list --repository git@github.com:org/repo.git --status READY",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			reviews, err := reviewpkg.RunReviewList(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review list planned.")
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
	cmd.Flags().StringVar(&params.Repository, "repository", "", "Filter by repository (any form git accepts, e.g. the output of `git remote get-url origin`)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Filter by target branch")
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Filter by source branch")
	cmd.Flags().StringVar(&params.Status, "status", "", "Filter by status: OPEN, CLOSED, FAILED, READY, MERGE, or MERGED")
	cmd.Flags().StringVar(&params.AuthorUserID, "author-user-id", "", "Filter by author user id")
	cmd.Flags().StringVar(&params.ReviewerUserID, "reviewer-user-id", "", "Filter by reviewer user id")
	cmd.Flags().BoolVar(&params.Mine, "mine", false, "Show only reviews you authored")
	cmd.Flags().BoolVar(&params.WaitingOnMe, "waiting-on-me", false, "Show only reviews you are a reviewer on")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writeReviewList(ctx core.Context, reviews []platformclient.PlatformReview) error {
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

// writeReviewLine renders one review. The repository is named on every line
// because a review's source/target pair does not tell two repositories a
// tenant serves apart, and a listing that renders them identically is how
// another tenant's -- or another repository's -- queue read as this one's.
// A review created before the platform recorded a repository says so rather
// than printing nothing, which would read as an empty value rather than an
// absent identity.
func writeReviewLine(ctx core.Context, review platformclient.PlatformReview) error {
	repository := review.Repository
	if strings.TrimSpace(repository) == "" {
		repository = "(no repository recorded)"
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) %s %s -> %s status=%s%s\n",
		review.Name, review.ReviewID, repository, review.SourceBranch, review.TargetBranch, review.Status,
		reviewIssueSuffix(review))
	return err
}

// reviewIssueSuffix renders the review's issue link and its provenance, or an
// empty string when it is linked to none.
//
// The provenance is not decoration: a reference parsed out of a branch name is
// a guess that the branch was named honestly, and printing it the way a
// declared one prints would tell an operator the author had recorded an issue
// they never mentioned. Both are shown, told apart.
func reviewIssueSuffix(review platformclient.PlatformReview) string {
	if strings.TrimSpace(review.IssueRef) == "" {
		return ""
	}
	source := "declared"
	if review.IssueRefSource == model.IssueReferenceInferred {
		source = "inferred from branch"
	}
	return fmt.Sprintf(" issue=%s (%s)", review.IssueRef, source)
}

func newReviewShowCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show REVIEW_ID",
		Short:        "Show a review, its comment threads, and its recorded builds",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			detail, err := reviewpkg.RunReviewShow(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review show planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewDetail(ctx, detail); err != nil {
					return err
				}
			}
			return ctx.WriteResult(detail)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writeReviewDetail(ctx core.Context, detail reviewpkg.ReviewDetail) error {
	if err := writeReviewLine(ctx, detail.Review); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "  builds: %d, comments: %d, unresolved threads: %d\n", len(detail.Builds), len(detail.Comments), detail.UnresolvedThreads); err != nil {
		return err
	}
	for _, comment := range detail.Comments {
		if err := writeReviewCommentLine(ctx, comment); err != nil {
			return err
		}
	}
	return nil
}

// writeReviewCommentLine renders one comment. A thread's status lives on its
// root comment only (a reply's own status is never separately settable), so
// only root lines carry status=.
func writeReviewCommentLine(ctx core.Context, comment platformclient.PlatformComment) error {
	if strings.TrimSpace(comment.ParentCommentID) != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "    ↳ [%s] %s:%d %s\n", comment.CommentID, comment.FilePath, comment.Line, comment.Body)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  [%s] status=%s %s:%d %s\n", comment.CommentID, comment.Status, comment.FilePath, comment.Line, comment.Body)
	return err
}

func newReviewCreateCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformCreateReviewParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Open a review on the erun platform",
		Long: "Open a review on the erun platform.\n\n" +
			"--name is the eventual squash-merge message and must be unique per tenant and repository; " +
			"a colliding name on a review that can still land fails with a conflict, while a CLOSED " +
			"review's name is free to reuse. --source-branch must already exist on the remote — push it " +
			"first with `erun exec push` — since the review references it by name and the platform " +
			"can only ever fetch what has actually landed there.\n\n" +
			"The review records the repository its branches belong to: --repository if given, otherwise " +
			"your checkout's origin. That identity is what keeps two repositories your tenant serves " +
			"from sharing one merge queue or colliding on a branch pair.\n\n" +
			"--issue records the issue this work belongs to, as owner/repo#number or as a bare number to " +
			"join to the recorded repository. A recorded issue is a declared one: it outranks the number " +
			"a branch like `bug/2212-...` names, which is only ever reported as an inferred link. Leave it " +
			"out and the branch is the whole link, exactly as before.\n\n" +
			"A real, immediate write, not a preview.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun exec push feature/add-widget\n" +
			"  erun review create --name \"Add widget\" --source-branch feature/add-widget --target-branch main\n" +
			"  erun review create --name \"Add widget\" --issue sophium/erun#2212 --source-branch feature/add-widget --target-branch main\n" +
			"  erun review create --name \"Add widget\" --repository https://github.com/org/repo.git --source-branch feature/add-widget --target-branch main",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			review, err := reviewpkg.RunReviewCreate(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review creation planned.")
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
	cmd.Flags().StringVar(&params.Repository, "repository", "", "Repository the branches belong to (defaults to the current checkout's origin; any form git accepts)")
	cmd.Flags().StringVar(&params.Name, "name", "", "Review name (unique per tenant and repository; the eventual squash-merge message)")
	cmd.Flags().StringVar(&params.TargetBranch, "target-branch", "", "Branch this review proposes merging into")
	cmd.Flags().StringVar(&params.SourceBranch, "source-branch", "", "Branch this review proposes merging (must already be pushed)")
	cmd.Flags().StringVar(&params.IssueRef, "issue", "", "Issue this work belongs to (owner/repo#number, or a bare number joined to the recorded repository)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewCommentCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
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
			ctx := shared.CommandContext(cmd)
			body, err := io.ReadAll(ctx.Stdin)
			if err != nil {
				return fmt.Errorf("read comment body from stdin: %w", err)
			}
			comment, err := reviewpkg.RunReviewComment(ctx, store, *alias, reviewpkg.ReviewCommentParams{
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
			if ctx.Output != core.OutputJSON {
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewResolveCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve REVIEW_ID COMMENT_ID",
		Short: "Resolve a comment thread by closing its root comment",
		Long: "Resolve a comment thread on a review by closing its root comment.\n\n" +
			"COMMENT_ID must be the thread's root comment — the first comment posted at a " +
			"file/line, not one made with --reply-to. Addressing a reply fails, naming the " +
			"root comment to retry against. A real, immediate write, not a preview.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Example:      "  erun review resolve 018f... 018g...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			comment, err := reviewpkg.RunReviewResolve(ctx, store, *alias, args[0], args[1], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review resolve planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewCommentLine(ctx, comment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(comment)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewUnresolveCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unresolve REVIEW_ID COMMENT_ID",
		Short: "Reopen a comment thread by marking its root comment OPEN",
		Long: "Reopen a comment thread on a review by marking its root comment OPEN again.\n\n" +
			"COMMENT_ID must be the thread's root comment — the first comment posted at a " +
			"file/line, not one made with --reply-to. Addressing a reply fails, naming the " +
			"root comment to retry against. A real, immediate write, not a preview.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Example:      "  erun review unresolve 018f... 018g...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			comment, err := reviewpkg.RunReviewUnresolve(ctx, store, *alias, args[0], args[1], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review unresolve planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writeReviewCommentLine(ctx, comment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(comment)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newReviewCloseCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "close REVIEW_ID",
		Short:        "Close a review without merging it",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			review, err := reviewpkg.RunReviewClose(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review close planned.")
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
	shared.AddDryRunFlag(cmd)
	return cmd
}
