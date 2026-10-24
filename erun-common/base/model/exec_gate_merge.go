package model

// GateMergeSource is one branch to squash, in landing order; Message is its squash
// commit message, with the branch's carried trailers appended beneath it.
type GateMergeSource struct {
	Branch  string
	Message string
}

// GateMergeWorkingTreeParams squash-merges Sources onto TargetBranch's current remote tip,
// one commit per landed source. Batching tests whether branches build together.
type GateMergeWorkingTreeParams struct {
	Sources []GateMergeSource
	// TargetBranch is checked out fresh from its remote tip, not the working tree's branch.
	TargetBranch string
	// Remote defaults to "origin".
	Remote string
	// UnderLeaseID names an exclusive environment claim the caller already holds.
	UnderLeaseID string
}

// GateMergeLandedSource is one source branch that squash-merged cleanly.
type GateMergeLandedSource struct {
	SourceBranch string `json:"sourceBranch"`
	SourceCommit string `json:"sourceCommit"`
	Commit       string `json:"commit"`
	// CarriedTrailers is always serialized, so "carried none" is distinguishable from "not read".
	CarriedTrailers []string `json:"carriedTrailers"`
}

// GateMergeSkippedSource is one source that did not land; a skip is reported, not fatal.
type GateMergeSkippedSource struct {
	SourceBranch    string   `json:"sourceBranch"`
	SourceCommit    string   `json:"sourceCommit,omitempty"`
	Reason          string   `json:"reason"`
	ConflictedFiles []string `json:"conflictedFiles,omitempty"`
}

// GateMergeWorkingTreeResult is what landed; Commit is the stack tip, or the target's tip if
// every source was skipped.
type GateMergeWorkingTreeResult struct {
	TargetBranch string                   `json:"targetBranch"`
	Remote       string                   `json:"remote"`
	Commit       string                   `json:"commit"`
	Landed       []GateMergeLandedSource  `json:"landed"`
	Skipped      []GateMergeSkippedSource `json:"skipped,omitempty"`
}
