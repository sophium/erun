package review

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// Merges go through erun's queue, so a pushed branch with no erun review can
// never land while the forge calls it clean. The notice is best-effort: it never
// fails a push that already landed.

// pushReviewNoticeTimeout does not bound the token mint (see buildReportTimeout).
const pushReviewNoticeTimeout = 15 * time.Second

// WarnPushedBranchWithoutReview warns on stderr when no non-closed review has
// branch as its sourceBranch; it never fails the caller. Reviews are named
// after the squash message, so only sourceBranch identifies the branch.
func WarnPushedBranchWithoutReview(ctx core.Context, store config.CloudReadStore, deps cloud.CloudDependencies, params model.PushedBranchReviewNoticeParams) {
	if !platformapi.HasAnyErunPlatformAlias(store) {
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
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("push review notice skipped: " + err.Error())
		return
	}
	filter := platformclient.PlatformReviewFilter{SourceBranch: branch}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews", platformclient.ReviewFilterTraceDetails(filter)...)
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

func pushedBranchWithoutReviewWarning(branch string) string {
	return "warning: pushed `" + branch + "`; no review references it — `erun review create` to queue it"
}

// branchHasLiveReview re-checks the source branch in case the plane ignores the
// filter.
func branchHasLiveReview(reviews []model.PlatformReview, branch string) bool {
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

// pushedBranchIsReviewTarget treats the remote's default branch as a target,
// not an orphan. The common-name fallback applies only when origin/HEAD is
// unreadable, since a false warning teaches operators to ignore it.
func pushedBranchIsReviewTarget(ctx core.Context, projectRoot, remote, branch string) bool {
	if strings.TrimSpace(projectRoot) != "" {
		if defaultBranch, ok := gitx.GitRemoteDefaultBranch(ctx, projectRoot, remote); ok {
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
