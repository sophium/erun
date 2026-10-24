package model

// IssueReferenceSource distinguishes a declared issue from one inferred from a branch name.
type IssueReferenceSource string

const (
	// IssueReferenceDeclared: the caller stated the issue.
	IssueReferenceDeclared IssueReferenceSource = "DECLARED"
	// IssueReferenceInferred is best-effort and must never be stored as declared.
	IssueReferenceInferred IssueReferenceSource = "INFERRED"
)

// IssueReference is the issue a piece of work belongs to and its provenance.
type IssueReference struct {
	Ref    string               `json:"ref"`
	Source IssueReferenceSource `json:"source"`
}
