package eruncommon

import (
	"regexp"
	"strings"
)

// issue_reference.go answers "which issue is this work for?" for work that did
// not say. A feature or a fix names its issue in the branch it proposes --
// root AGENTS.md documents `feature/<issue-number>-<description>` and
// `bug/<issue-number>-<description>` -- and that convention is the only thing
// linking a review to its issue, because nothing on the platform records one.
//
// The derivation is best-effort by design, and the provenance travels with the
// answer. A number parsed out of a branch name is a guess that the branch was
// named honestly, and a caller that renders it as a link the author declared
// claims a provenance erun does not have. IssueReferenceSource is what stops
// that, so it is part of the answer rather than decoration on it.

// branchIssueNumberPattern is the branch half of root AGENTS.md's branching
// strategy. Only the prefix and the number carry the link, so the description
// after the number is deliberately unvalidated: erun enforces no shape on it
// anywhere, and a branch whose slug reads oddly is still a branch for that
// issue.
var branchIssueNumberPattern = regexp.MustCompile(`^(?:bug|feature)/(\d+)-`)

// IssueReferenceSource says where an issue reference came from. It is the
// difference between "the author said so" and "the branch name implies it",
// and a caller that drops it presents both as the same claim.
type IssueReferenceSource string

const (
	// IssueReferenceDeclared: the issue was stated, and the link is as
	// authoritative as the caller that stated it.
	IssueReferenceDeclared IssueReferenceSource = "DECLARED"
	// IssueReferenceInferred: the issue was parsed out of a branch name under
	// the documented convention. Best-effort, and never to be written back as
	// though it had been declared.
	IssueReferenceInferred IssueReferenceSource = "INFERRED"
)

// IssueReference is an issue a piece of work belongs to, and where the link
// came from.
type IssueReference struct {
	// Ref is the issue the work belongs to: the declared reference verbatim,
	// or the number the branch names.
	Ref string `json:"ref"`
	// Source is how Ref was obtained.
	Source IssueReferenceSource `json:"source"`
}

// BranchIssueNumber returns the issue number sourceBranch names under the
// documented branch convention, or "" when it follows none. An empty answer is
// the correct one for a branch that merely contains digits: deriving a link
// from it would attach work to an issue nobody named.
func BranchIssueNumber(sourceBranch string) string {
	matches := branchIssueNumberPattern.FindStringSubmatch(strings.TrimSpace(sourceBranch))
	if matches == nil {
		return ""
	}
	return matches[1]
}

// ResolveIssueReference answers which issue a piece of work belongs to: the
// declared reference when there is one, and otherwise the number the source
// branch names. ok is false when neither names an issue, which is a review
// with no issue link rather than a review linked to a guess.
//
// A declared reference wins over a derivable one outright. The two disagreeing
// is exactly the case worth stating a rule for -- a review whose branch looks
// like one issue's while its author recorded another's is a renamed branch or
// a deliberate reassignment, and neither is resolved by overruling the caller.
func ResolveIssueReference(declared, sourceBranch string) (IssueReference, bool) {
	if trimmed := strings.TrimSpace(declared); trimmed != "" {
		return IssueReference{Ref: trimmed, Source: IssueReferenceDeclared}, true
	}
	if number := BranchIssueNumber(sourceBranch); number != "" {
		return IssueReference{Ref: number, Source: IssueReferenceInferred}, true
	}
	return IssueReference{}, false
}
