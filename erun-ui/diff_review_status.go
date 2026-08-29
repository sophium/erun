package main

import (
	"context"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// diff_review_status.go resolves the diff panel's review-status chip: where
// a source/target branch pair sits on the review ladder, reusing the exact
// platform reads the Reviews tab and merge queue already make
// (loadTenantDashboardReviews, loadTenantDashboardReviewThreadCounts,
// loadReviewDetailQueuePosition) rather than a parallel query path. Before
// this, StartReviewFromDiffAction rendered one fixed "Start a review" button
// unconditionally and discovered an existing review only from the 409
// submitCreateReview's catch block reports, after the operator had already
// committed and pushed.

// Chip states this side ever returns. The frontend adds one more of its own,
// "checking" (set locally while this call is in flight) -- "unavailable" is
// the honest not-yet-known answer from this side, distinct from "none",
// which is a confirmed one (no live review for this pair), so the chip never
// renders an absence as a fact it has not actually established.
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

// uiDiffReviewStatus is the chip's own read model. CanAdvanceMergeQueue lets
// the diff panel's "Advance queue" action degrade by permission the same way
// the Reviews tab's does, instead of rendering an enabled control that fails
// with a 403 after the click.
type uiDiffReviewStatus struct {
	State                string `json:"state"`
	PlatformState        string `json:"platformState,omitempty"`
	ReviewID             string `json:"reviewId,omitempty"`
	Name                 string `json:"name,omitempty"`
	QueuePosition        int    `json:"queuePosition,omitempty"`
	UnresolvedThreads    *int   `json:"unresolvedThreads,omitempty"`
	LastFailedBuildID    string `json:"lastFailedBuildId,omitempty"`
	LastMergedBuildID    string `json:"lastMergedBuildId,omitempty"`
	CanAdvanceMergeQueue bool   `json:"canAdvanceMergeQueue"`
}

// DiffReviewStatus resolves the chip for one source/target branch pair. A
// missing precondition (no tenant, no resolvable branch) reports "unavailable"
// rather than erroring: this is a background enrichment read the diff panel
// makes for every environment section, not a caller-driven action, so a
// precondition gap is a state to render, not a failure to surface as one.
func (a *App) DiffReviewStatus(input uiDiffReviewStatusInput) (uiDiffReviewStatus, error) {
	tenant := strings.TrimSpace(input.Tenant)
	sourceBranch := strings.TrimSpace(input.SourceBranch)
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if tenant == "" || sourceBranch == "" || targetBranch == "" {
		return uiDiffReviewStatus{State: diffReviewStateUnavailable}, nil
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()

	platform, early, err := a.resolveDiffReviewPlatform(requestCtx, tenant)
	if err != nil {
		return uiDiffReviewStatus{}, err
	}
	if early != nil {
		return *early, nil
	}

	reviews, err := platform.client.ListReviews(requestCtx, eruncommon.PlatformReviewFilter{
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
		LastFailedBuildID:    review.LastFailedBuildID,
		LastMergedBuildID:    review.LastMergedBuildID,
		CanAdvanceMergeQueue: platform.canAdvance,
	}
	applyDiffReviewLadderState(requestCtx, platform.client, platform.capabilities, review, &status)
	return status, nil
}

// diffReviewPlatform is the ready-to-use client and capabilities
// resolveDiffReviewPlatform resolved, once past every state where the chip
// has nothing further to read.
type diffReviewPlatform struct {
	client       *eruncommon.PlatformClient
	capabilities eruncommon.PlatformCapabilities
	canAdvance   bool
}

// resolveDiffReviewPlatform resolves the platform client and the caller's
// capabilities, or an early status to return as-is: the platform not being
// ready, or the caller lacking read access to reviews at all -- "a read the
// caller may not make is not attempted" (erun-ui/AGENTS.md, Professional UX).
func (a *App) resolveDiffReviewPlatform(ctx context.Context, tenant string) (diffReviewPlatform, *uiDiffReviewStatus, error) {
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	if resolution.state != tenantPlatformStateReady {
		return diffReviewPlatform{}, &uiDiffReviewStatus{State: diffReviewStateUnavailable, PlatformState: resolution.state}, nil
	}
	whoami, err := resolution.client.Whoami(ctx)
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	canAdvance := restrictedTenantDashboardRead(whoami.Capabilities, tenantDashboardWriteAdvanceMergeQueue) == ""
	if restricted := restrictedTenantDashboardRead(whoami.Capabilities, tenantDashboardReadReviews); restricted != "" {
		return diffReviewPlatform{}, &uiDiffReviewStatus{
			State:                diffReviewStateUnavailable,
			PlatformState:        tenantPlatformStateNoPermission,
			CanAdvanceMergeQueue: canAdvance,
		}, nil
	}
	return diffReviewPlatform{client: resolution.client, capabilities: whoami.Capabilities, canAdvance: canAdvance}, nil, nil
}

// applyDiffReviewLadderState fills in the state a review actually occupies.
func applyDiffReviewLadderState(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, review eruncommon.PlatformReview, status *uiDiffReviewStatus) {
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

// applyDiffReviewReadyState resolves what a READY review actually is from the
// chip's vantage: Blocked when its comment threads are unresolved (mirrors
// AdvanceMergeQueueAction's own gate -- merge-queue/advance refuses the same
// review for the same reason), Ready with its queue position otherwise.
func applyDiffReviewReadyState(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, review eruncommon.PlatformReview, status *uiDiffReviewStatus) {
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadComments) == "" {
		if comments, err := client.ListComments(ctx, review.ReviewID); err == nil {
			count := eruncommon.CountUnresolvedThreads(comments)
			status.UnresolvedThreads = &count
		}
	}
	if status.UnresolvedThreads != nil && *status.UnresolvedThreads > 0 {
		status.State = diffReviewStateBlocked
		return
	}
	status.State = diffReviewStateReady
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadMergeQueue) == "" {
		if queue, err := client.ListMergeQueue(ctx, review.TargetBranch); err == nil {
			status.QueuePosition = reviewQueuePosition(queue, review.ReviewID)
		}
	}
}

// mostRecentPlatformReview picks the pair's own current review: ListReviews
// can return more than one across the pair's history (a prior review closed
// or merged, then reopened), and the most recently updated one is the one the
// chip should describe.
func mostRecentPlatformReview(reviews []eruncommon.PlatformReview) (eruncommon.PlatformReview, bool) {
	var best eruncommon.PlatformReview
	found := false
	for _, review := range reviews {
		if !found || review.UpdatedAt.After(best.UpdatedAt) {
			best = review
			found = true
		}
	}
	return best, found
}
