package model

// PushedBranchReviewNoticeParams names the push that just landed.
type PushedBranchReviewNoticeParams struct {
	// ProjectRoot resolves the remote's default branch; empty falls back to the
	// common default-branch names.
	ProjectRoot string
	Branch      string
	// Remote defaults to "origin".
	Remote string
}
