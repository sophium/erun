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
		newReviewResolveCmd(store, &alias, deps),
		newReviewUnresolveCmd(store, &alias, deps),
		newReviewCloseCmd(store, &alias, deps),
		newReviewRecordBuildCmd(store, &alias, deps),
		newReviewReportMergedCmd(store, &alias, deps),
		newReviewReviewersCmd(store, &alias, deps),
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
func writeReviewCommentLine(ctx common.Context, comment common.PlatformComment) error {
	if strings.TrimSpace(comment.ParentCommentID) != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "    ↳ [%s] %s:%d %s\n", comment.CommentID, comment.FilePath, comment.Line, comment.Body)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  [%s] status=%s %s:%d %s\n", comment.CommentID, comment.Status, comment.FilePath, comment.Line, comment.Body)
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

func newReviewResolveCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
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
			ctx := commandContext(cmd)
			comment, err := common.RunReviewResolve(ctx, store, *alias, args[0], args[1], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review resolve planned.")
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
	addDryRunFlag(cmd)
	return cmd
}

func newReviewUnresolveCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
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
			ctx := commandContext(cmd)
			comment, err := common.RunReviewUnresolve(ctx, store, *alias, args[0], args[1], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review unresolve planned.")
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

func newReviewRecordBuildCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var (
		commitID      string
		gate          bool
		version       string
		failed        bool
		failureDetail string
	)
	cmd := &cobra.Command{
		Use:   "record-build REVIEW_ID",
		Short: "Record a build against a review, moving it to READY or FAILED",
		Long: "Record a build against a review. This is the only way an erun client transitions a review off " +
			"OPEN: recording a successful build moves it to READY (and, if it was already the merge queue's " +
			"head, on to MERGE); recording a failed one moves it to FAILED. There is no separate command to set " +
			"a review's status directly to READY or FAILED — only a recorded build result does that.\n\n" +
			"commit must be the full 40-character commit hash the build ran against (e.g. from `git rev-parse " +
			"HEAD` after pushing), and version the version it minted (from `erun build --release --output " +
			"json`), even for a failed build — release resolves the version before the build step runs.\n\n" +
			"--gate records the merge queue's own GATE build kind instead of an ordinary build: the environment " +
			"a review's merge queue promotes to MERGE runs `erun build` (never --release) against the " +
			"prospective merge and reports the result this way. A GATE build carries no version, since the gate " +
			"publishes nothing — omit --version when --gate is set. Only a successful GATE build can later be " +
			"reported MERGED with `erun review report-merged`.\n\n" +
			"A real, immediate write. --dry-run traces the call without making it.",
		Example: "  erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3\n" +
			"  erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3 --failed --failure-detail 'image build failed'\n" +
			"  erun review record-build 018f... --commit $(git rev-parse HEAD) --gate",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			build, err := common.RunReviewRecordBuild(ctx, store, *alias, common.ReviewRecordBuildParams{
				ReviewID:      args[0],
				CommitID:      commitID,
				Gate:          gate,
				Version:       version,
				Successful:    !failed,
				FailureDetail: failureDetail,
			}, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review record-build planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewBuildLine(ctx, build); err != nil {
					return err
				}
			}
			return ctx.WriteResult(build)
		},
	}
	cmd.Flags().StringVar(&commitID, "commit", "", "Full commit hash the build ran against")
	cmd.Flags().BoolVar(&gate, "gate", false, "Record the merge queue's own GATE build kind instead of an ordinary build")
	cmd.Flags().StringVar(&version, "version", "", "Version the build minted (from erun build --release); omit with --gate")
	cmd.Flags().BoolVar(&failed, "failed", false, "Record the build as failed instead of successful")
	cmd.Flags().StringVar(&failureDetail, "failure-detail", "", "Why the build failed (only meaningful with --failed)")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewReportMergedCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var (
		buildID   string
		remoteURL string
	)
	cmd := &cobra.Command{
		Use:   "report-merged REVIEW_ID",
		Short: "Report a review MERGED after gate-building and pushing its prospective merge",
		Long: "Report a review MERGED. This is for the environment a review's merge queue promoted to MERGE, " +
			"once it has fetched the review's target and source, gate-built the prospective squash merge with " +
			"`erun review record-build --gate`, and pushed the result — never before the push actually landed.\n\n" +
			"The platform does not take this report on trust: it checks buildId names an already-recorded, " +
			"successful GATE build for this review, then fetches remoteUrl to confirm that build's commit is " +
			"really reachable from the target branch's tip with the parent this review was gated against. Any " +
			"of those checks failing refuses with 409 MERGE_NOT_VERIFIED and leaves the review at MERGE.\n\n" +
			"A real, immediate write. --dry-run traces the call without making it.",
		Example:      "  erun review report-merged 018f... --build-id 018e... --remote-url https://github.com/org/repo.git",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			review, err := common.RunReviewReportMerged(ctx, store, *alias, args[0], buildID, remoteURL, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review report-merged planned.")
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
	cmd.Flags().StringVar(&buildID, "build-id", "", "The successful GATE build's id")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The git remote the platform fetches to verify the merge")
	addDryRunFlag(cmd)
	return cmd
}

func writeReviewBuildLine(ctx common.Context, build common.PlatformBuild) error {
	_, err := fmt.Fprintf(ctx.Stdout, "  [%s] successful=%t commit=%s version=%s\n",
		build.BuildID, build.Successful, build.CommitID, build.Version)
	return err
}

func newReviewReviewersCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"reviewers",
		"Assign and remove reviewers on a review",
		newReviewReviewersListCmd(store, alias, deps),
		newReviewReviewersAddCmd(store, alias, deps),
		newReviewReviewersRemoveCmd(store, alias, deps),
	)
}

func writeReviewerLine(ctx common.Context, reviewer common.PlatformReviewer) error {
	_, err := fmt.Fprintf(ctx.Stdout, "  - %s\n", reviewer.UserID)
	return err
}

func newReviewReviewersListCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list REVIEW_ID",
		Short:        "List a review's assigned reviewers",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review reviewers list 018f...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			reviewers, err := common.RunReviewReviewersList(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers list planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
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
	addDryRunFlag(cmd)
	return cmd
}

func newReviewReviewersAddCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
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
			ctx := commandContext(cmd)
			reviewer, err := common.RunReviewReviewerAdd(ctx, store, *alias, args[0], userID, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers add planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if err := writeReviewerLine(ctx, reviewer); err != nil {
					return err
				}
			}
			return ctx.WriteResult(reviewer)
		},
	}
	cmd.Flags().StringVar(&userID, "user-id", "", "User id to assign as a reviewer (required)")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewReviewersRemoveCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var userID string
	cmd := &cobra.Command{
		Use:          "remove REVIEW_ID",
		Short:        "Remove a reviewer from a review",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review reviewers remove 018f... --user-id 018g...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			err := common.RunReviewReviewerRemove(ctx, store, *alias, args[0], userID, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review reviewers remove planned.")
				return err
			}
			if ctx.Output != common.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "removed reviewer %s\n", userID); err != nil {
					return err
				}
			}
			return ctx.WriteResult(map[string]string{"reviewId": args[0], "userId": userID})
		},
	}
	cmd.Flags().StringVar(&userID, "user-id", "", "User id to remove as a reviewer (required)")
	addDryRunFlag(cmd)
	return cmd
}

func newReviewMergeQueueCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"queue",
		"Inspect and advance a target branch's merge queue",
		newReviewMergeQueueListCmd(store, alias, deps),
		newReviewMergeQueueAdvanceCmd(store, alias, deps),
		newReviewMergeQueueOverrideAdvanceCmd(store, alias, deps),
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
		Short: "Advance a target branch's merge queue head to MERGE",
		Long: "Advance a target branch's merge queue head to MERGE, which starts that review's merge-gate build.\n\n" +
			"A real, immediate mutation of shared control-plane state: it fails if the queue is " +
			"empty or its head is not READY, and refuses with the unresolved comment thread count " +
			"when the head still has open threads — resolve them first, or use " +
			"`erun review queue override-advance`.",
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

func newReviewMergeQueueOverrideAdvanceCmd(store common.CloudReadStore, alias *string, deps common.CloudDependencies) *cobra.Command {
	var targetBranch, reason string
	cmd := &cobra.Command{
		Use:   "override-advance",
		Short: "Bypass the unresolved-thread gate and advance the merge queue anyway",
		Long: "Bypass `erun review queue advance`'s unresolved-thread gate and advance a target " +
			"branch's merge queue head to MERGE anyway.\n\n" +
			"--reason is required and is recorded in the platform's audit trail alongside the " +
			"caller's identity — this is a deliberate, accountable escape hatch, not a routine " +
			"way to advance the queue. A real, immediate mutation of shared control-plane state.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun review queue override-advance --target-branch main --reason \"hotfix, reviewers unavailable\"",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := commandContext(cmd)
			review, err := common.RunReviewMergeQueueOverrideAdvance(ctx, store, *alias, targetBranch, reason, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review queue override-advance planned.")
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
	cmd.Flags().StringVar(&reason, "reason", "", "Why the unresolved-thread gate is being bypassed (required)")
	addDryRunFlag(cmd)
	return cmd
}
