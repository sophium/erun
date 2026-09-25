package routes

import (
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	eruncommon "github.com/sophium/erun/erun-common"
)

// review_issue_ref.go attaches a review's issue reference to the reviews this
// API returns.
//
// A review's issue link is resolved on read from two sources: the reference
// its author recorded on the review itself, and — when there is none — the
// number its source branch names under the documented convention. The two are
// marked differently for that reason: a branch name is a guess that it was
// named honestly, and it is reported as inferred. This is output adaptation
// and belongs in the route layer: it changes what a review looks like on the
// wire, never what is stored, and the repository and service layers keep
// seeing the row as it is.

// resolveReviewIssueRef resolves one review's issue reference, preferring the
// reference its author recorded over whatever the source branch looks like —
// the rule erun-common's resolver already holds.
//
// Canonicity is the platform's, never the caller's: the stored reference was
// normalized to owner/repo#number by PrepareCreate before it was written, so
// what reaches the DECLARED branch here is already the spelling every other
// pipeline uses. A caller cannot make a branch-derived number arrive as
// declared — createReviewRequest carries no issueRefSource field — and this
// function is the only writer of a returned review's
// IssueRef/IssueRefSource.
func resolveReviewIssueRef(review model.Review) (eruncommon.IssueReference, bool) {
	return eruncommon.ResolveIssueReference(review.DeclaredIssueRef, review.SourceBranch)
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
