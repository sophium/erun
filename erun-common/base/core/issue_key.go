package core

import (
	"fmt"
	"regexp"
	"strings"
)

// canonicalIssueKeyPattern is owner/repo#number, the form jobs.issue_ref holds.
var canonicalIssueKeyPattern = regexp.MustCompile(`^[^/\s#]+/[^/\s#]+#\d+$`)

var bareIssueNumberPattern = regexp.MustCompile(`^\d+$`)

// RepositorySlug reduces a RepositoryIdentity to owner/repo, or reports false
// rather than guess when the identity is not exactly a forge owner and repo.
func RepositorySlug(identity string) (string, bool) {
	trimmed := strings.TrimSpace(identity)
	if trimmed == "" {
		return "", false
	}
	scheme, rest, hasScheme := strings.Cut(trimmed, "://")
	if !hasScheme {
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

// CanonicalIssueKey returns ref as owner/repo#number: a canonical ref verbatim,
// a bare number joined to repository's slug, and false for anything else.
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

// NormalizeIssueRef canonicalizes a declared issue reference; empty is allowed,
// and a bare number needs a repository to join.
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
