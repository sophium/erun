package eruncommon

import (
	"context"
	"strings"
	"time"
)

// push_review_notice.go makes one silent state visible at the moment it is
// created: a branch that has just been pushed and that no review references.
//
// This repository merges through erun's own queue rather than the forge, so a
// pushed branch carrying a forge pull request but no erun review registers
// nothing, gates nothing, and can never land -- while the forge reports that
// pull request as mergeable and clean. Nothing anywhere surfaced that state:
// the queue read as healthy and empty, and the divergence grew for as long as
// nobody hand-cross-referenced two listings by branch name.
//
// The check is best-effort on purpose, holding the same contract as
// ReportBuildOutcome: it is a signal, never a gate. No configured platform
// alias degrades completely silently, and every other reason the check cannot
// complete is traced and returned from, so a notice that could not be produced
// never fails a push that already landed. The plane is known to be flaky,
// which is the whole reason the check is allowed to give up quietly rather
// than insist.

// pushReviewNoticeTimeout bounds the review lookup so an unreachable or slow
// plane cannot hang a push that has already succeeded. It does not bound the
// token mint a request triggers first when no cached access token exists (the
// gap buildReportTimeout documents, shared by every platform command here).
const pushReviewNoticeTimeout = 15 * time.Second

// PushedBranchReviewNoticeParams names the push that just landed.
type PushedBranchReviewNoticeParams struct {
	// ProjectRoot is the working tree the push ran from, used to resolve the
	// remote's default branch so pushing that branch is never reported as an
	// orphan. Empty skips that one read; the common default-branch names still
	// apply.
	ProjectRoot string
	// Branch is the branch the push actually landed, as the push reported it.
	Branch string
	// Remote is the remote pushed to. Empty means "origin".
	Remote string
}

// WarnPushedBranchWithoutReview prints a warning when branch has no review
// referencing it, so an inert push is visible at the moment it happens rather
// than after the work has accumulated outside the queue. It returns nothing
// and never fails its caller: the warning goes to the caller's own stderr and
// the push's exit status is unchanged, because an unqueued branch is a state
// to surface, not an operation to refuse.
//
// The review lookup is a real query rather than a guess from the branch name:
// `erun review create` names a review after the eventual squash message, so
// the review's sourceBranch is the only field that identifies the branch the
// review is for. Any non-closed review counts, whatever queue state it is in,
// because a closed review is the only one that references nothing anymore.
//
// Traces the intended call before checking ctx.DryRun, the same contract
// tracePlatformCall's other callers use, so --dry-run shows the check without
// making it.
func WarnPushedBranchWithoutReview(ctx Context, store CloudReadStore, deps CloudDependencies, params PushedBranchReviewNoticeParams) {
	if !hasAnyErunPlatformAlias(store) {
		return
	}
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		ctx.Trace("push review notice skipped: no branch resolved to check")
		return
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	if pushedBranchIsReviewTarget(ctx, params.ProjectRoot, remote, branch) {
		ctx.Trace("push review notice skipped: " + branch + " is the remote's default branch, which originates work rather than targeting it")
		return
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("push review notice skipped: " + err.Error())
		return
	}
	filter := PlatformReviewFilter{SourceBranch: branch}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews", reviewFilterTraceDetails(filter)...)
	if ctx.DryRun {
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), pushReviewNoticeTimeout)
	defer cancel()
	reviews, err := client.ListReviews(timeoutCtx, filter)
	if err != nil {
		ctx.Trace("push review notice skipped: " + err.Error())
		return
	}
	if branchHasLiveReview(reviews, branch) {
		return
	}
	ctx.Info(pushedBranchWithoutReviewWarning(branch))
}

// pushedBranchWithoutReviewWarning is the one line an operator sees. It names
// the branch and the exact next command, because the state it reports is
// invisible from every other surface: the forge calls the branch's pull
// request clean and the queue calls itself empty.
func pushedBranchWithoutReviewWarning(branch string) string {
	return "warning: pushed `" + branch + "`; no review references it — `erun review create` to queue it"
}

// branchHasLiveReview reports whether any of reviews is a review of branch
// that has not been closed. The list is filtered by the platform already; the
// source-branch match is repeated here so a plane that ignores the filter
// cannot be read as "this branch has a review" for some other branch.
func branchHasLiveReview(reviews []PlatformReview, branch string) bool {
	for _, review := range reviews {
		if review.Status == "CLOSED" {
			continue
		}
		if strings.TrimSpace(review.SourceBranch) == branch {
			return true
		}
	}
	return false
}

// pushedBranchIsReviewTarget reports whether branch is the branch work pushed
// from here would be reviewed against: the remote's own default branch, which
// originates work rather than targeting it, so pushing it is not an orphan.
// The remote's default branch is authoritative when origin/HEAD's symref can
// be read; the common names cover a clone whose symref was never set (a
// shallow or single-branch fetch), the same fallback
// environmentJobWorktreeIsProtectedBranch uses.
//
// A false positive here is worse than a missed warning -- an operator told to
// queue a branch that does not need queueing learns to ignore the line -- so
// the fallback only fires when the symref is genuinely unavailable.
func pushedBranchIsReviewTarget(ctx Context, projectRoot, remote, branch string) bool {
	if strings.TrimSpace(projectRoot) != "" {
		if defaultBranch, ok := gitRemoteDefaultBranch(ctx, projectRoot, remote); ok {
			return branch == defaultBranch
		}
	}
	switch branch {
	case "main", "master", "develop":
		return true
	default:
		return false
	}
}
