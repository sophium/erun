package main

import (
	"context"
	"strings"
	"sync"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// loadTenantDashboardReviewFilterCounts reports how many reviews are Mine and
// how many are Waiting on me, independent of whichever filter the caller
// currently has applied: the Reviews tab's filter buttons need the
// distribution visible before the caller clicks either one, not only
// after. Best effort like the other row-enrichment reads: a restricted or
// failing count simply leaves the corresponding field unset rather than
// reporting a false zero.
func loadTenantDashboardReviewFilterCounts(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard, userID string) {
	if strings.TrimSpace(userID) == "" || restrictedTenantDashboardRead(capabilities, tenantDashboardReadReviews) != "" {
		return
	}
	if mine, err := client.ListReviews(ctx, platformclient.PlatformReviewFilter{AuthorUserID: userID}); err == nil {
		count := len(mine)
		dashboard.MineReviewCount = &count
	}
	if waiting, err := client.ListReviews(ctx, platformclient.PlatformReviewFilter{ReviewerUserID: userID}); err == nil {
		count := len(waiting)
		dashboard.WaitingOnMeReviewCount = &count
	}
}

// reviewsLoadOutcome carries the Reviews panel's own result forward to the
// Builds panel, which needs the same review list but must not blank on a
// restriction or failure the Reviews panel already reported for a different
// reason than its own.
type reviewsLoadOutcome struct {
	reviews    []platformclient.PlatformReview
	err        error
	restricted string
}

func loadTenantDashboardReviews(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard, filter platformclient.PlatformReviewFilter, usernames map[string]string) reviewsLoadOutcome {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabReviews}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadReviews); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return reviewsLoadOutcome{restricted: restricted}
	}
	reviews, err := client.ListReviews(ctx, filter)
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadReviews, err)
		dashboard.Panels = append(dashboard.Panels, panel)
		return reviewsLoadOutcome{err: err}
	}
	dashboard.Reviews = tenantDashboardReviews(reviews, usernames)
	dashboard.Panels = append(dashboard.Panels, panel)
	return reviewsLoadOutcome{reviews: reviews}
}

// loadTenantDashboardReviewThreadCounts enriches each review row with its
// unresolved-thread count, so "is this review actually finished" is readable
// from the list itself, not only inside the detail dialog. A review is left
// without a count (rather than a false zero) when the caller cannot read
// comments at all, or when its own comment read failed — supplemental detail
// on a row, not worth a panel error the way the Reviews/Builds panels get.
func loadTenantDashboardReviewThreadCounts(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard, reviewsOutcome reviewsLoadOutcome) {
	if reviewsOutcome.restricted != "" || reviewsOutcome.err != nil {
		return
	}
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadComments) != "" {
		return
	}
	counts := fetchReviewThreadCountsConcurrently(ctx, client, reviewsOutcome.reviews)
	for i := range dashboard.Reviews {
		count, ok := counts[dashboard.Reviews[i].ReviewID]
		if !ok {
			continue
		}
		dashboard.Reviews[i].UnresolvedThreads = &count
	}
}

// maxConcurrentReviewCommentReads mirrors maxConcurrentReviewBuildReads for
// the same reason: bound the per-review /comments reads a big tenant's
// listing pays for, inside the one dashboard load's shared timeout.
const maxConcurrentReviewCommentReads = 8

// fetchReviewThreadCountsConcurrently reads every review's comments in
// parallel, bounded by maxConcurrentReviewCommentReads, mirroring
// fetchReviewBuildsConcurrently's shape. A review whose comment read fails is
// simply absent from the returned map rather than reported as zero.
func fetchReviewThreadCountsConcurrently(ctx context.Context, client *platformclient.PlatformClient, reviews []platformclient.PlatformReview) map[string]int {
	counts := make(map[string]int, len(reviews))
	semaphore := make(chan struct{}, maxConcurrentReviewCommentReads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		wg.Add(1)
		semaphore <- struct{}{}
		go func(reviewID string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			comments, err := client.ListComments(ctx, reviewID)
			if err != nil {
				return
			}
			count := reviewpkg.CountUnresolvedThreads(comments)
			mu.Lock()
			counts[reviewID] = count
			mu.Unlock()
		}(reviewID)
	}
	wg.Wait()
	return counts
}

// maxConcurrentReviewBuildReads bounds how many /builds requests run at once,
// so a tenant with many reviews still fits comfortably inside one request's
// share of tenantDashboardTimeout instead of paying N sequential round trips.
const maxConcurrentReviewBuildReads = 8

// fetchReviewBuildsConcurrently reads every review's builds in parallel,
// bounded by maxConcurrentReviewBuildReads, and flattens the results in the
// review order. Every read runs to completion regardless of another review's
// failure, so the result keeps every review that succeeded and still reports
// the first failure, independent of where in the list it landed.
func fetchReviewBuildsConcurrently(ctx context.Context, client *platformclient.PlatformClient, reviews []platformclient.PlatformReview) ([]uiTenantDashboardBuild, error) {
	perReview := make([][]platformclient.PlatformBuild, len(reviews))
	semaphore := make(chan struct{}, maxConcurrentReviewBuildReads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, review := range reviews {
		reviewID := strings.TrimSpace(review.ReviewID)
		if reviewID == "" {
			continue
		}
		wg.Add(1)
		semaphore <- struct{}{}
		go func(index int, reviewID string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			reviewBuilds, err := client.ListBuilds(ctx, reviewID)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			perReview[index] = reviewBuilds
		}(i, reviewID)
	}
	wg.Wait()

	builds := make([]uiTenantDashboardBuild, 0, len(reviews))
	for i, review := range reviews {
		for _, build := range perReview[i] {
			builds = append(builds, uiTenantDashboardBuild{
				BuildID:    build.BuildID,
				TenantID:   build.TenantID,
				ReviewID:   build.ReviewID,
				ReviewName: strings.TrimSpace(review.Name),
				Successful: build.Successful,
				CommitID:   build.CommitID,
				Version:    build.Version,
				Profile:    build.Profile,
				CreatedAt:  tenantDashboardTime(build.CreatedAt),
				UpdatedAt:  tenantDashboardTime(build.UpdatedAt),
			})
		}
	}
	return builds, firstErr
}

func tenantDashboardReviews(reviews []platformclient.PlatformReview, usernames map[string]string) []uiTenantDashboardReview {
	converted := make([]uiTenantDashboardReview, 0, len(reviews))
	for _, review := range reviews {
		converted = append(converted, tenantDashboardReviewWithUsername(review, usernames))
	}
	return converted
}

// tenantDashboardReviewWithUsername mirrors tenantDashboardCommentWithUsername:
// the read paths (listings, single-review load) have a resolved user
// directory to enrich with; the write paths in tenant_review_write.go return
// the review the caller themselves just acted on and use the base converter
// directly, unresolved.
func tenantDashboardReviewWithUsername(review platformclient.PlatformReview, usernames map[string]string) uiTenantDashboardReview {
	converted := tenantDashboardReview(review)
	converted.AuthorUsername = usernames[review.AuthorUserID]
	return converted
}

func tenantDashboardReview(review platformclient.PlatformReview) uiTenantDashboardReview {
	return uiTenantDashboardReview{
		ReviewID:          review.ReviewID,
		TenantID:          review.TenantID,
		AuthorUserID:      review.AuthorUserID,
		Name:              review.Name,
		Repository:        review.Repository,
		TargetBranch:      review.TargetBranch,
		SourceBranch:      review.SourceBranch,
		Status:            review.Status,
		LastFailedBuildID: review.LastFailedBuildID,
		LastReadyBuildID:  review.LastReadyBuildID,
		LastMergedBuildID: review.LastMergedBuildID,
		CreatedAt:         tenantDashboardTime(review.CreatedAt),
		UpdatedAt:         tenantDashboardTime(review.UpdatedAt),
	}
}
