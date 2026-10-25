package main

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
)

// Resolves the diff panel's review-status chip from the same platform reads the Reviews tab uses.

// Chip states returned here; the frontend adds "checking". "unavailable" means not
// yet known, unlike "none", which is confirmed.
const (
	diffReviewStateUnavailable = "unavailable"
	diffReviewStateNone        = "none"
	diffReviewStateOpen        = "open"
	diffReviewStateReady       = "ready"
	diffReviewStateBlocked     = "blocked"
	diffReviewStateFailed      = "failed"
	diffReviewStateMerging     = "merging"
	diffReviewStateMerged      = "merged"
	diffReviewStateClosed      = "closed"
)

type uiDiffReviewStatusInput struct {
	Tenant       string `json:"tenant"`
	SourceBranch string `json:"sourceBranch"`
	TargetBranch string `json:"targetBranch"`
}

// uiDiffReviewStatus is the chip's read model; CanAdvanceMergeQueue avoids a control that would 403.
type uiDiffReviewStatus struct {
	State         string `json:"state"`
	PlatformState string `json:"platformState,omitempty"`
	ReviewID      string `json:"reviewId,omitempty"`
	Name          string `json:"name,omitempty"`
	// Repository targets this review's own queue, not another repo sharing the target branch.
	Repository           string `json:"repository,omitempty"`
	QueuePosition        int    `json:"queuePosition,omitempty"`
	UnresolvedThreads    *int   `json:"unresolvedThreads,omitempty"`
	LastFailedBuildID    string `json:"lastFailedBuildId,omitempty"`
	LastMergedBuildID    string `json:"lastMergedBuildId,omitempty"`
	CanAdvanceMergeQueue bool   `json:"canAdvanceMergeQueue"`
}

// DiffReviewStatus resolves the chip for a branch pair. Missing preconditions report
// "unavailable" rather than an error: this is a background enrichment read.
func (a *App) DiffReviewStatus(input uiDiffReviewStatusInput) (uiDiffReviewStatus, error) {
	tenant := strings.TrimSpace(input.Tenant)
	sourceBranch := strings.TrimSpace(input.SourceBranch)
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if tenant == "" || sourceBranch == "" || targetBranch == "" {
		return uiDiffReviewStatus{State: diffReviewStateUnavailable}, nil
	}
	ctx := a.context()
	requestCtx, cancel := context.WithTimeout(ctx, tenantsvc.RequestTimeout)
	defer cancel()

	platform, early, err := a.resolveDiffReviewPlatform(requestCtx, tenant)
	if err != nil {
		return uiDiffReviewStatus{}, err
	}
	if early != nil {
		return *early, nil
	}

	reviews, err := platform.client.ListReviews(requestCtx, platformclient.PlatformReviewFilter{
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return uiDiffReviewStatus{}, err
	}
	review, found := mostRecentPlatformReview(reviews)
	if !found {
		return uiDiffReviewStatus{State: diffReviewStateNone, CanAdvanceMergeQueue: platform.canAdvance}, nil
	}

	status := uiDiffReviewStatus{
		ReviewID:             review.ReviewID,
		Name:                 review.Name,
		Repository:           review.Repository,
		LastFailedBuildID:    review.LastFailedBuildID,
		LastMergedBuildID:    review.LastMergedBuildID,
		CanAdvanceMergeQueue: platform.canAdvance,
	}
	applyDiffReviewLadderState(requestCtx, platform.client, platform.capabilities, review, &status)
	return status, nil
}

type diffReviewPlatform struct {
	client       *platformclient.PlatformClient
	capabilities model.PlatformCapabilities
	canAdvance   bool
}

// resolveDiffReviewPlatform returns the client and capabilities, or an early status when
// the platform is not ready or reviews are not readable (erun-ui/AGENTS.md, Professional UX).
func (a *App) resolveDiffReviewPlatform(ctx context.Context, tenant string) (diffReviewPlatform, *uiDiffReviewStatus, error) {
	resolution, err := a.tenant.Platform(tenant, "")
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	if resolution.State != tenantsvc.StateReady {
		return diffReviewPlatform{}, &uiDiffReviewStatus{State: diffReviewStateUnavailable, PlatformState: resolution.State}, nil
	}
	whoami, err := resolution.Client.Whoami(ctx)
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	canAdvance := tenantsvc.RestrictedRead(whoami.Capabilities, tenantsvc.WriteAdvanceMergeQueue) == ""
	if restricted := tenantsvc.RestrictedRead(whoami.Capabilities, tenantsvc.ReadReviews); restricted != "" {
		return diffReviewPlatform{}, &uiDiffReviewStatus{
			State:                diffReviewStateUnavailable,
			PlatformState:        tenantsvc.StateNoPermission,
			CanAdvanceMergeQueue: canAdvance,
		}, nil
	}
	return diffReviewPlatform{client: resolution.Client, capabilities: whoami.Capabilities, canAdvance: canAdvance}, nil, nil
}

func applyDiffReviewLadderState(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review model.PlatformReview, status *uiDiffReviewStatus) {
	switch strings.ToUpper(strings.TrimSpace(review.Status)) {
	case "OPEN":
		status.State = diffReviewStateOpen
	case "FAILED":
		status.State = diffReviewStateFailed
	case "MERGE":
		status.State = diffReviewStateMerging
	case "MERGED":
		status.State = diffReviewStateMerged
	case "CLOSED":
		status.State = diffReviewStateClosed
	case "READY":
		applyDiffReviewReadyState(ctx, client, capabilities, review, status)
	default:
		status.State = diffReviewStateOpen
	}
}

// applyDiffReviewReadyState marks a READY review Blocked when threads are unresolved,
// matching merge-queue/advance's gate; otherwise Ready with its queue position.
func applyDiffReviewReadyState(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review model.PlatformReview, status *uiDiffReviewStatus) {
	if tenantsvc.RestrictedRead(capabilities, tenantsvc.ReadComments) == "" {
		if comments, err := client.ListComments(ctx, review.ReviewID); err == nil {
			count := reviewpkg.CountUnresolvedThreads(comments)
			status.UnresolvedThreads = &count
		}
	}
	if status.UnresolvedThreads != nil && *status.UnresolvedThreads > 0 {
		status.State = diffReviewStateBlocked
		return
	}
	status.State = diffReviewStateReady
	if tenantsvc.RestrictedRead(capabilities, tenantsvc.ReadMergeQueue) == "" {
		if queue, err := client.ListMergeQueue(ctx, model.PlatformMergeQueueParams{
			Repository: review.Repository, TargetBranch: review.TargetBranch,
		}); err == nil {
			status.QueuePosition = tenantsvc.QueuePosition(queue, review.ReviewID)
		}
	}
}

// mostRecentPlatformReview picks the most recently updated of the pair's reviews.
func mostRecentPlatformReview(reviews []model.PlatformReview) (model.PlatformReview, bool) {
	var best model.PlatformReview
	found := false
	for _, review := range reviews {
		if !found || review.UpdatedAt.After(best.UpdatedAt) {
			best = review
			found = true
		}
	}
	return best, found
}
