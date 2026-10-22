package eruncommon

import (
	"fmt"
	"regexp"
	"strings"
)

// issue_key.go defines the platform's canonical spelling of an issue:
// owner/repo#number, the form jobs.issue_ref already uses.
//
// The two pipelines that carry work spell an issue differently. A job states
// its issue outright, in that canonical form. A review used to state nothing
// at all, and its link was the bare number its source branch names under the
// documented branch convention; a declared reference is now recorded on the
// review, but the branch-derived one still has no repository in it. A view
// spanning both needs one key to join them on, and a bare number is not one:
// two repositories a tenant serves can each have an issue with the same
// number, and the number alone says which of them nothing about.
//
// The join is therefore the review's own recorded repository identity
// (RepositoryIdentity) reduced to the owner/repo slug an issue is addressed
// with, plus the number the branch names. Where that slug cannot be read out
// of the identity -- a file remote, a local path, a forge address with no
// owner/repo path -- the view is told so rather than handed a guess: the
// answer is "no canonical key", which renders as work that names no issue.

// canonicalIssueKeyPattern is the canonical spelling: two non-empty path
// segments and a number, with no whitespace anywhere. It is the shape the
// console already links from (its issue URL builder) and the shape
// jobs.issue_ref is documented to hold.
var canonicalIssueKeyPattern = regexp.MustCompile(`^[^/\s#]+/[^/\s#]+#\d+$`)

// bareIssueNumberPattern is the review side's own spelling: the number a
// source branch carries, with the repository left to be supplied.
var bareIssueNumberPattern = regexp.MustCompile(`^\d+$`)

// RepositorySlug reduces a canonical repository identity
// (eruncommon.RepositoryIdentity) to the owner/repo an issue on it is
// addressed with, and reports false when the identity names no such pair.
//
// Only a remote that names a forge with exactly an owner and a repository
// path gives one. A file remote, a local path, and a deeper path all answer
// false rather than guessing which segments are the owner: an issue key built
// from a guess sends a reader to an issue nobody named, which is the failure
// the whole derivation is built to avoid.
func RepositorySlug(identity string) (string, bool) {
	trimmed := strings.TrimSpace(identity)
	if trimmed == "" {
		return "", false
	}
	scheme, rest, hasScheme := strings.Cut(trimmed, "://")
	if !hasScheme {
		// A bare local path is not a forge address.
		return "", false
	}
	if scheme == "file" {
		return "", false
	}
	authority, path, hasPath := strings.Cut(rest, "/")
	if authority == "" || !hasPath {
		return "", false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", false
	}
	return segments[0] + "/" + segments[1], true
}

// CanonicalIssueKey renders the canonical owner/repo#number key for a work
// item's issue reference, given the repository (RepoIdentity form) the work
// belongs to.
//
// A reference already in canonical form is returned verbatim: it names its
// own repository, and re-deriving it from the work item's repository would
// silently rewrite a link the author stated. A bare number is joined to the
// work item's repository slug. Anything else -- and a bare number whose
// repository has no slug -- answers false, which is "this work names no
// canonical issue", never a fabricated key.
func CanonicalIssueKey(ref, repository string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", false
	}
	if canonicalIssueKeyPattern.MatchString(trimmed) {
		return trimmed, true
	}
	if !bareIssueNumberPattern.MatchString(trimmed) {
		return "", false
	}
	slug, ok := RepositorySlug(repository)
	if !ok {
		return "", false
	}
	return slug + "#" + trimmed, true
}

// NormalizeIssueRef resolves a caller-declared issue reference to the
// canonical spelling, so what a review records is the same key a job's
// issue_ref holds and a view can join the two on.
//
// An empty declaration is not an error: it is a review whose link, if any, is
// the one its source branch names. A bare number is accepted when the
// repository is known, because that is the spelling the branch convention
// already teaches; without a repository there is nothing to join it to, and
// refusing names the form to use instead rather than storing a number no
// other pipeline can match.
func NormalizeIssueRef(declared, repository string) (string, error) {
	trimmed := strings.TrimSpace(declared)
	if trimmed == "" {
		return "", nil
	}
	if canonicalIssueKeyPattern.MatchString(trimmed) {
		return trimmed, nil
	}
	if bareIssueNumberPattern.MatchString(trimmed) {
		if slug, ok := RepositorySlug(repository); ok {
			return slug + "#" + trimmed, nil
		}
		return "", fmt.Errorf("issue %q names no repository: use owner/repo#number, or record a repository to join the number to", trimmed)
	}
	return "", fmt.Errorf("issue %q is not an issue reference: use owner/repo#number, or a bare issue number with a repository", trimmed)
}
