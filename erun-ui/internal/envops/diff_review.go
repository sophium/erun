package envops

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
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

type DiffReviewStatusInput struct {
	Tenant       string `json:"tenant"`
	SourceBranch string `json:"sourceBranch"`
	TargetBranch string `json:"targetBranch"`
}

// DiffReviewStatus is the chip's read model; CanAdvanceMergeQueue avoids a control that would 403.
type DiffReviewStatus struct {
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
func (s *Service) DiffReviewStatus(input DiffReviewStatusInput) (DiffReviewStatus, error) {
	tenant := strings.TrimSpace(input.Tenant)
	sourceBranch := strings.TrimSpace(input.SourceBranch)
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if tenant == "" || sourceBranch == "" || targetBranch == "" {
		return DiffReviewStatus{State: diffReviewStateUnavailable}, nil
	}
	ctx := s.d.Context()
	requestCtx, cancel := context.WithTimeout(ctx, s.d.Review.RequestTimeout)
	defer cancel()

	platform, early, err := s.resolveDiffReviewPlatform(requestCtx, tenant)
	if err != nil {
		return DiffReviewStatus{}, err
	}
	if early != nil {
		return *early, nil
	}

	reviews, err := platform.client.ListReviews(requestCtx, platformclient.PlatformReviewFilter{
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return DiffReviewStatus{}, err
	}
	review, found := mostRecentPlatformReview(reviews)
	if !found {
		return DiffReviewStatus{State: diffReviewStateNone, CanAdvanceMergeQueue: platform.canAdvance}, nil
	}

	status := DiffReviewStatus{
		ReviewID:             review.ReviewID,
		Name:                 review.Name,
		Repository:           review.Repository,
		LastFailedBuildID:    review.LastFailedBuildID,
		LastMergedBuildID:    review.LastMergedBuildID,
		CanAdvanceMergeQueue: platform.canAdvance,
	}
	s.applyDiffReviewLadderState(requestCtx, platform.client, platform.capabilities, review, &status)
	return status, nil
}

type diffReviewPlatform struct {
	client       *platformclient.PlatformClient
	capabilities model.PlatformCapabilities
	canAdvance   bool
}

// resolveDiffReviewPlatform returns the client and capabilities, or an early status when
// the platform is not ready or reviews are not readable (erun-ui/AGENTS.md, Professional UX).
func (s *Service) resolveDiffReviewPlatform(ctx context.Context, tenant string) (diffReviewPlatform, *DiffReviewStatus, error) {
	state, client, err := s.d.ReviewPlatform(tenant)
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	if state != "" {
		return diffReviewPlatform{}, &DiffReviewStatus{State: diffReviewStateUnavailable, PlatformState: state}, nil
	}
	whoami, err := client.Whoami(ctx)
	if err != nil {
		return diffReviewPlatform{}, nil, err
	}
	canAdvance := s.d.RestrictedRead(whoami.Capabilities, s.d.Review.AdvanceMergeQueue) == ""
	if restricted := s.d.RestrictedRead(whoami.Capabilities, s.d.Review.ReadReviews); restricted != "" {
		return diffReviewPlatform{}, &DiffReviewStatus{
			State:                diffReviewStateUnavailable,
			PlatformState:        s.d.Review.NoPermissionState,
			CanAdvanceMergeQueue: canAdvance,
		}, nil
	}
	return diffReviewPlatform{client: client, capabilities: whoami.Capabilities, canAdvance: canAdvance}, nil, nil
}

func (s *Service) applyDiffReviewLadderState(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review model.PlatformReview, status *DiffReviewStatus) {
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
		s.applyDiffReviewReadyState(ctx, client, capabilities, review, status)
	default:
		status.State = diffReviewStateOpen
	}
}

// applyDiffReviewReadyState marks a READY review Blocked when threads are unresolved,
// matching merge-queue/advance's gate; otherwise Ready with its queue position.
func (s *Service) applyDiffReviewReadyState(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review model.PlatformReview, status *DiffReviewStatus) {
	if s.d.RestrictedRead(capabilities, s.d.Review.ReadComments) == "" {
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
	if s.d.RestrictedRead(capabilities, s.d.Review.ReadMergeQueue) == "" {
		if queue, err := client.ListMergeQueue(ctx, model.PlatformMergeQueueParams{
			Repository: review.Repository, TargetBranch: review.TargetBranch,
		}); err == nil {
			status.QueuePosition = s.d.QueuePosition(queue, review.ReviewID)
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
