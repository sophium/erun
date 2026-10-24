package model

// MergeWorkingTreeBranchParams names the branch to merge in. Always a merge, never a rebase:
// review comments anchor to commit IDs.
type MergeWorkingTreeBranchParams struct {
	// TargetBranch is resolved against Remote, e.g. "origin/main".
	TargetBranch string
	// Remote defaults to "origin".
	Remote string
}

// MergeWorkingTreeBranchResult is what actually landed.
type MergeWorkingTreeBranchResult struct {
	Branch       string `json:"branch"`
	TargetBranch string `json:"targetBranch"`
	Remote       string `json:"remote"`
	Commit       string `json:"commit"`
}
