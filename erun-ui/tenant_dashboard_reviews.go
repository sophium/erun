package main

import (
	"context"
	"strings"
	"sync"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// loadTenantDashboardReviewFilterCounts counts Mine and Waiting regardless of the
// active filter; a failed count stays unset, never a false zero.
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

// reviewsLoadOutcome lets the Builds panel reuse the review list without inheriting its restriction.
type reviewsLoadOutcome struct {
	reviews    []model.PlatformReview
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

// A review without a readable comment list gets no count rather than a false zero.
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

const maxConcurrentReviewCommentReads = 8

func fetchReviewThreadCountsConcurrently(ctx context.Context, client *platformclient.PlatformClient, reviews []model.PlatformReview) map[string]int {
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

// maxConcurrentReviewBuildReads keeps a big tenant within tenantDashboardTimeout.
const maxConcurrentReviewBuildReads = 8

// Every read runs to completion; the first failure is reported in review order.
func fetchReviewBuildsConcurrently(ctx context.Context, client *platformclient.PlatformClient, reviews []model.PlatformReview) ([]uiTenantDashboardBuild, error) {
	perReview := make([][]model.PlatformBuild, len(reviews))
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

func tenantDashboardReviews(reviews []model.PlatformReview, usernames map[string]string) []uiTenantDashboardReview {
	converted := make([]uiTenantDashboardReview, 0, len(reviews))
	for _, review := range reviews {
		converted = append(converted, tenantDashboardReviewWithUsername(review, usernames))
	}
	return converted
}

// Write paths use the base converter unresolved.
func tenantDashboardReviewWithUsername(review model.PlatformReview, usernames map[string]string) uiTenantDashboardReview {
	converted := tenantDashboardReview(review)
	converted.AuthorUsername = usernames[review.AuthorUserID]
	return converted
}

func tenantDashboardReview(review model.PlatformReview) uiTenantDashboardReview {
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
