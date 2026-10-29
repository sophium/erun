package model

// A gate-merge squash commit is not the branch head GitHub tracks, so GitHub
// never closes the pull request itself; this does, after report-merged.

// ClosePullRequestParams is the `erun exec close-pr` input.
type ClosePullRequestParams struct {
	RemoteURL string
	Branch    string
	// TargetBranch disambiguates when Branch has open pull requests against
	// several bases.
	TargetBranch string
	// GatedCommit is Branch's tip when the merge drive fetched it; closing is
	// refused if the pull request head has moved since.
	GatedCommit   string
	LandingCommit string
}

// ClosePullRequestResult is what actually happened.
type ClosePullRequestResult struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	// Found is false for a queued plain branch, which is not an error.
	Found  bool `json:"found"`
	Number int  `json:"number,omitempty"`
	Closed bool `json:"closed"`
}
