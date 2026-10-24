package review

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

func newReviewRecordBuildCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var (
		commitID      string
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
			"HEAD` after pushing), and version the version it minted — from the run's own `erun build --output " +
			"json`, or from `erun build --dry-run --output json` when it failed before printing one — even for a " +
			"failed build.\n\n" +
			"A RECORDED build asserts the commit builds; it publishes nothing, so the version is metadata no " +
			"platform path resolves. A `--release` produced version is accepted but not required: building the " +
			"pushed branch with `erun build` and recording what it mints is the ordinary path, and the artifact " +
			"that ships is cut after merge by the release the accepted review enqueues. A release here publishes " +
			"a two-architecture `-pr.<sha>` image set per pull request that nothing consumes.\n\n" +
			"The merge queue records its build the same way: the environment a review's merge queue promotes " +
			"to MERGE runs a plain `erun build` (never --release) of the prospective merge commit `erun exec " +
			"gate-merge` prepared and records it here with the version it minted. Only a successful build of " +
			"the merge commit that landed can later be reported MERGED with `erun review report-merged`.\n\n" +
			"A --failed whose --failure-detail names a known erun infrastructure failure (a registry or the " +
			"network giving up, e.g. a ghcr.io TLS handshake timeout) is refused outright: a build has no " +
			"INCONCLUSIVE result, so recording it FAILED would move the review out of the merge queue for a " +
			"network blip rather than a real defect. Re-run the build once the signature clears and record " +
			"that result instead.\n\n" +
			"A real, immediate write. --dry-run traces the call without making it.",
		Example: "  erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3\n" +
			"  erun review record-build 018f... --commit $(git rev-parse HEAD) --version 1.2.3 --failed --failure-detail 'image build failed'",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			build, err := reviewpkg.RunReviewRecordBuild(ctx, store, *alias, model.ReviewRecordBuildParams{
				ReviewID:      args[0],
				CommitID:      commitID,
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
			if ctx.Output != model.OutputJSON {
				if err := writeReviewBuildLine(ctx, build); err != nil {
					return err
				}
			}
			return ctx.WriteResult(build)
		},
	}
	cmd.Flags().StringVar(&commitID, "commit", "", "Full commit hash the build ran against")
	cmd.Flags().StringVar(&version, "version", "", "Version the build minted (from erun build --output json, or --dry-run --output json when it failed) (required)")
	cmd.Flags().BoolVar(&failed, "failed", false, "Record the build as failed instead of successful")
	cmd.Flags().StringVar(&failureDetail, "failure-detail", "", "Why the build failed (only meaningful with --failed)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func newReviewReportMergedCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var (
		buildID   string
		remoteURL string
	)
	cmd := &cobra.Command{
		Use:   "report-merged REVIEW_ID",
		Short: "Report a review MERGED, for a queue-driven merge or work that landed elsewhere",
		Long: "Report a review MERGED. Which verification the platform applies depends on where the review is " +
			"sitting, not on what this command claims.\n\n" +
			"A review at MERGE is the merge queue's: this is for the environment the queue promoted to MERGE, " +
			"once it has fetched the review's target and source, built the prospective squash merge, recorded " +
			"that build with `erun review record-build`, and pushed the result — never before the push actually " +
			"landed. --build-id must name that successful recorded build of the merge commit, and the platform " +
			"fetches --remote-url to confirm the build's commit is really reachable from the target branch's tip " +
			"and descends from, without being, the target tip this review was queued against. Any of those " +
			"checks failing refuses with 409 MERGE_NOT_VERIFIED and leaves the review at MERGE.\n\n" +
			"Any other review is one whose work landed without the queue — in practice a GitHub squash merge, " +
			"where the branch's own commits are not ancestors of the target and no merge-commit build exists to name, " +
			"or a branch that landed by merge commit or fast-forward, including one the target was fast-forwarded " +
			"onto: that ancestry is confirmed directly. " +
			"Omit --build-id: the platform confirms against the same remote that everything the review's source " +
			"branch adds is already present in the target branch's history, and moves the review only if it is. " +
			"A branch that did not land is refused just as firmly, with the same MERGE_NOT_VERIFIED. This is " +
			"what keeps a squash-landed review from sitting OPEN forever and inflating the open count.\n\n" +
			"--remote-url must name the review's own repository, in any form git accepts: the platform " +
			"canonicalizes it, so an SSH remote verifies against the HTTPS identity the review recorded, and " +
			"a remote for a different repository is refused rather than used to verify. A review created " +
			"before the platform recorded a repository adopts the one named here.\n\n" +
			"A real, immediate write. --dry-run traces the call without making it.",
		Example: "  erun review report-merged 018f... --build-id 018e... --remote-url https://github.com/org/repo.git\n" +
			"  erun review report-merged 018f... --remote-url https://github.com/org/repo.git",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			review, err := reviewpkg.RunReviewReportMerged(ctx, store, *alias, args[0], buildID, remoteURL, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review report-merged planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	cmd.Flags().StringVar(&buildID, "build-id", "", "The successful recorded build of the merge commit that landed (omit for work that landed without the queue)")
	cmd.Flags().StringVar(&remoteURL, "remote-url", "", "The git remote the platform fetches to verify the merge; any form git accepts, an SSH remote being read over its host's HTTPS without credentials")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func newReviewRequeueCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "requeue REVIEW_ID",
		Short: "Move a review stuck at MERGE back to READY",
		Long: "Move a review stuck at MERGE back to READY, freeing its target branch's merge-queue slot so a " +
			"different review can be promoted (only one review may be at MERGE per target branch).\n\n" +
			"This is for a review whose queued build never finishes, or one left at MERGE by a batched " +
			"`erun exec gate-merge` whose other members landed but were never promoted (erun#2241). The requeued " +
			"review rejoins the queue at the tail, not the head — it is not promoted again immediately. Refuses, " +
			"naming the review's actual status, when it is not at MERGE.\n\n" +
			"A real, immediate write. --dry-run traces the call without making it.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun review requeue 018f...",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			review, err := reviewpkg.RunReviewRequeue(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun review requeue planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writeReviewLine(ctx, review); err != nil {
					return err
				}
			}
			return ctx.WriteResult(review)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func writeReviewBuildLine(ctx core.Context, build model.PlatformBuild) error {
	_, err := fmt.Fprintf(ctx.Stdout, "  [%s] successful=%t commit=%s version=%s\n",
		build.BuildID, build.Successful, build.CommitID, build.Version)
	return err
}
