package model

// PushWorkingTreeBranchParams names the intended branch; it is verified before pushing.
type PushWorkingTreeBranchParams struct {
	Branch string
	// Remote defaults to "origin".
	Remote string
}

// PushWorkingTreeBranchResult is what actually landed.
type PushWorkingTreeBranchResult struct {
	Branch string `json:"branch"`
	Remote string `json:"remote"`
	Commit string `json:"commit"`
}
