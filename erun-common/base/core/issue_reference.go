package core

import (
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// branchIssueNumberPattern matches root AGENTS.md's branch naming; the
// description after the number is not validated.
var branchIssueNumberPattern = regexp.MustCompile(`^(?:bug|feature)/(\d+)-`)

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
func ResolveIssueReference(declared, sourceBranch string) (model.IssueReference, bool) {
	if trimmed := strings.TrimSpace(declared); trimmed != "" {
		return model.IssueReference{Ref: trimmed, Source: model.IssueReferenceDeclared}, true
	}
	if number := BranchIssueNumber(sourceBranch); number != "" {
		return model.IssueReference{Ref: number, Source: model.IssueReferenceInferred}, true
	}
	return model.IssueReference{}, false
}
