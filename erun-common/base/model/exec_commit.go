package model

// CommitWorkingTreeParams names the intended commit; Branch is verified before staging.
type CommitWorkingTreeParams struct {
	Branch string
	// Message is recorded verbatim; nothing in it is interpreted.
	Message string
	// Paths, when set, scopes staging and refuses changes outside it so unrelated edits
	// are never absorbed; a blank entry is refused rather than widening to `git add -A`.
	Paths []string
	// SkipHooks uses --no-verify, only for machine commits (checkpoints, gate-merge squash)
	// where a hook rejection would destroy work of a run that already ended.
	SkipHooks bool
}

// CommitWorkingTreeResult is what actually landed.
type CommitWorkingTreeResult struct {
	Branch string   `json:"branch"`
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
}
