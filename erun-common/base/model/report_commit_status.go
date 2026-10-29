package model

// The status targets the source-branch tip, not the squash-merge commit:
// GitHub evaluates required checks only on commits reachable from the open
// pull request.

// ReportCommitStatusState is a state GitHub's Statuses API accepts.
type ReportCommitStatusState string

const (
	CommitStatusSuccess ReportCommitStatusState = "success"
	CommitStatusFailure ReportCommitStatusState = "failure"
	CommitStatusError   ReportCommitStatusState = "error"
	CommitStatusPending ReportCommitStatusState = "pending"
)

// ReportCommitStatusParams is the `erun exec report-commit-status` input.
type ReportCommitStatusParams struct {
	RemoteURL string
	Commit    string
	State     ReportCommitStatusState
	// Context defaults to DefaultCommitStatusContext.
	Context     string
	Description string
	TargetURL   string
}

// ReportCommitStatusResult is what was actually reported.
type ReportCommitStatusResult struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
	Context string `json:"context"`
}
