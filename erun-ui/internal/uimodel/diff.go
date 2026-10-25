package uimodel

// DiffOptions is the diff panel's request, shared by the pod diff and the host diffs.
type DiffOptions struct {
	Scope          string `json:"scope,omitempty"`
	SelectedCommit string `json:"selectedCommit,omitempty"`
	// Target "" or "env" diffs the runtime workdir; "erun" the contribute clone.
	Target string `json:"target,omitempty"`
}
