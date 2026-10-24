package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// reconcilingReview wires a review sitting at status whose branch the
// verifier will answer for, so a test can drive reconcileMerged without a
// real remote.
func reconcilingReview(status model.ReviewStatus, verifier fakeMergeVerifier) (*fakeReviewRepo, *ReviewService) {
	reviews := newFakeReviewRepo(model.Review{ReviewID: "review-1", TargetBranch: "main", SourceBranch: "bug/thing", Status: status})
	svc := NewReviewService(reviews, &fakeReviewBuilds{},
		&fakeReviewComments{byReview: map[string][]model.Comment{}}, &fakeReviewAudit{}, verifier, nil)
	return reviews, svc
}

// TestReconcileMergedAcceptsALandedBranchFromAnyOpenStatus is the transition
// erun#2575 is about: a review whose work landed without the queue — a GitHub
// squash merge — reaches MERGED from wherever it was sitting, on the
// verifier's word that the branch's changes really are in the target. Before
// this it could never move at all, so it sat OPEN forever and made the OPEN
// count grow by one for every change that landed that way.
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

// TestReconcileMergedRefusesWhenTheBranchIsNotInTheTarget: the reconciliation
// verifies rather than believes — a branch that did not land is refused, and
// the review is left exactly where it was.
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

// TestReconcileMergedRefusesWithNoVerifier: without a way to check the real
// repository there is nothing to reconcile against, the same fail-closed
// stance verifyRepositoryState takes for a queue-driven merge.
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

// TestReconcileMergedRefusesAClosedReview: CLOSED is terminal — reconciliation
// is for the review that is stuck open, not for reopening a decision already
// made.
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
