package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

func reconcilingReview(status model.ReviewStatus, verifier fakeMergeVerifier) (*fakeReviewRepo, *ReviewService) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "bug/thing", Status: status})
	svc := NewReviewService(reviews, &fakeReviewBuilds{},
		&fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, verifier, nil)
	return reviews, svc
}

func TestReconcileMergedAcceptsALandedBranchFromAnyOpenStatus(t *testing.T) {
	for _, status := range []model.ReviewStatus{model.ReviewStatusOpen, model.ReviewStatusReady, model.ReviewStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			reviews, svc := reconcilingReview(status, fakeMergeVerifier{contained: true, landingCommit: "squash-1"})

			updated, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
			if err != nil {
				t.Fatalf("UpdateStatus(MERGED) from %s error = %v, want it reconciled", status, err)
			}
			if updated.Status != model.ReviewStatusMerged {
				t.Fatalf("returned status = %s, want MERGED", updated.Status)
			}
			if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusMerged {
				t.Fatalf("persisted status = %s, want MERGED", got)
			}
			if got := reviews.reviews["review-1"].LastMergedBuildID; got != "" {
				t.Fatalf("lastMergedBuildId = %q, want it empty: a reconciliation has no build", got)
			}
		})
	}
}

func TestReconcileMergedRefusesWhenTheBranchIsNotInTheTarget(t *testing.T) {
	reviews, svc := reconcilingReview(model.ReviewStatusOpen, fakeMergeVerifier{contained: false})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("error = %v, want it to unwrap to ErrInvalidInput", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusOpen {
		t.Fatalf("status = %s, want the review left at OPEN, not moved to MERGED", got)
	}
}

func TestReconcileMergedRefusesWithNoVerifier(t *testing.T) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "bug/thing", Status: model.ReviewStatusOpen})
	svc := NewReviewService(reviews, &fakeReviewBuilds{},
		&fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, nil, nil)

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "")
	var notVerified *MergeNotVerifiedError
	if !errors.As(err, &notVerified) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *MergeNotVerifiedError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusOpen {
		t.Fatalf("status = %s, want the review left at OPEN", got)
	}
}

func TestReconcileMergedRefusesAClosedReview(t *testing.T) {
	reviews, svc := reconcilingReview(model.ReviewStatusClosed, fakeMergeVerifier{contained: true})

	_, err := svc.UpdateStatus(context.Background(), "review-1", model.ReviewStatusMerged, "", "file:///remote.git")
	var invalidTransition *InvalidTransitionError
	if !errors.As(err, &invalidTransition) {
		t.Fatalf("UpdateStatus(MERGED) error = %v, want *InvalidTransitionError", err)
	}
	if got := reviews.reviews["review-1"].Status; got != model.ReviewStatusClosed {
		t.Fatalf("status = %s, want the review left at CLOSED", got)
	}
}
