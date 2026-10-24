package routes

import (
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// Issue links are resolved on read and never stored: a declared reference wins,
// otherwise the source branch's number is reported as inferred.

// resolveReviewIssueRef is the only writer of a returned review's IssueRef/IssueRefSource.
func resolveReviewIssueRef(review model.Review) (modelpkg.IssueReference, bool) {
	return modelpkg.ResolveIssueReference(review.DeclaredIssueRef, review.SourceBranch)
}

func withReviewIssueRef(review model.Review) model.Review {
	resolved, ok := resolveReviewIssueRef(review)
	if !ok {
		return review
	}
	review.IssueRef = resolved.Ref
	review.IssueRefSource = resolved.Source
	return review
}

// withReviewsIssueRef copies the slice; a read must not rewrite the repository's reviews.
func withReviewsIssueRef(reviews []model.Review) []model.Review {
	if len(reviews) == 0 {
		return reviews
	}
	resolved := make([]model.Review, len(reviews))
	for i, review := range reviews {
		resolved[i] = withReviewIssueRef(review)
	}
	return resolved
}
