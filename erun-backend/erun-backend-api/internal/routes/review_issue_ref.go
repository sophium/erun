package routes

import (
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	eruncommon "github.com/sophium/erun/erun-common"
)

// review_issue_ref.go attaches a review's issue reference to the reviews this
// API returns.
//
// A review's issue link is resolved, not stored. Nothing on the reviews table
// records one, so the branch-name convention is the only source there is, and
// the answer is marked inferred for that reason. This is output adaptation and
// belongs in the route layer: it changes what a review looks like on the wire,
// never what is stored, and the repository and service layers keep seeing the
// row as it is.

// resolveReviewIssueRef resolves one review's issue reference. The declared
// side is empty because a review carries no issue of its own yet; the moment
// one is recorded, it is passed here and wins over whatever the source branch
// looks like, which is the rule erun-common's resolver already holds.
func resolveReviewIssueRef(review model.Review) (eruncommon.IssueReference, bool) {
	return eruncommon.ResolveIssueReference("", review.SourceBranch)
}

// withReviewIssueRef returns review with its resolved issue reference
// attached, and unchanged when it has none.
func withReviewIssueRef(review model.Review) model.Review {
	resolved, ok := resolveReviewIssueRef(review)
	if !ok {
		return review
	}
	review.IssueRef = resolved.Ref
	review.IssueRefSource = resolved.Source
	return review
}

// withReviewsIssueRef applies the same resolution to a listing, over a fresh
// slice: a read must not rewrite the reviews the repository handed back.
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
