package model

import (
	"regexp"
	"strings"
)

// branchIssueNumberPattern matches root AGENTS.md's branch naming; the
// description after the number is not validated.
var branchIssueNumberPattern = regexp.MustCompile(`^(?:bug|feature)/(\d+)-`)

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

// BranchIssueNumber returns the issue number in sourceBranch, or "" when it
// does not follow the convention.
func BranchIssueNumber(sourceBranch string) string {
	matches := branchIssueNumberPattern.FindStringSubmatch(strings.TrimSpace(sourceBranch))
	if matches == nil {
		return ""
	}
	return matches[1]
}

// ResolveIssueReference prefers the declared reference over the branch-derived
// one; ok is false when neither names an issue.
func ResolveIssueReference(declared, sourceBranch string) (IssueReference, bool) {
	if trimmed := strings.TrimSpace(declared); trimmed != "" {
		return IssueReference{Ref: trimmed, Source: IssueReferenceDeclared}, true
	}
	if number := BranchIssueNumber(sourceBranch); number != "" {
		return IssueReference{Ref: number, Source: IssueReferenceInferred}, true
	}
	return IssueReference{}, false
}
