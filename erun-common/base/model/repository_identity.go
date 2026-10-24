package model

import (
	"fmt"
	"strings"
)

// RepositoryIdentity canonicalizes a git remote so SSH and HTTPS spellings of
// one repository yield the same identity.
func RepositoryIdentity(remote string) (string, error) {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return "", fmt.Errorf("a repository remote is required: pass the repository's own git remote, e.g. the output of `git remote get-url origin`")
	}
	if !namesOneRepository(trimmed) {
		return "", fmt.Errorf("repository remote %q names no repository: a bare owner/repo shorthand, or a path relative to the caller's own working tree, is a different repository to every caller — pass the repository's own git remote, e.g. the output of `git remote get-url origin`", trimmed)
	}
	canonical := trimmed
	if httpsForm, ok := sshRemoteHTTPSForm(trimmed); ok {
		canonical = httpsForm
	}
	canonical = strings.TrimSuffix(strings.TrimRight(canonical, "/"), ".git")
	canonical = strings.TrimRight(canonical, "/")
	canonical = lowercasedRemoteSchemeAndHost(canonical)
	// A bare host names a forge, not a repository; a file remote has no authority.
	if scheme, rest, hasScheme := strings.Cut(canonical, "://"); hasScheme {
		authority, path, hasPath := strings.Cut(rest, "/")
		if authority == "" && scheme != "file" {
			return "", fmt.Errorf("repository remote %q names no repository", trimmed)
		}
		if !hasPath || path == "" {
			return "", fmt.Errorf("repository remote %q names no repository", trimmed)
		}
	}
	if canonical == "" {
		return "", fmt.Errorf("repository remote %q names no repository", trimmed)
	}
	return canonical, nil
}

// namesOneRepository rejects remotes whose meaning depends on the caller, such
// as `owner/repo` shorthand or relative paths.
func namesOneRepository(remote string) bool {
	if strings.Contains(remote, "://") {
		return true
	}
	if _, ok := scpLikeHTTPSForm(remote); ok {
		return true
	}
	return isAbsoluteLocalPath(remote)
}

// isAbsoluteLocalPath reports whether path is a rooted POSIX or Windows drive path.
func isAbsoluteLocalPath(path string) bool {
	if strings.HasPrefix(path, "/") {
		return true
	}
	if len(path) < 3 || path[1] != ':' {
		return false
	}
	drive := path[0]
	if ('a' > drive || drive > 'z') && ('A' > drive || drive > 'Z') {
		return false
	}
	return path[2] == '/' || path[2] == '\\'
}

// sshRemoteHTTPSForm maps an SSH remote to its HTTPS form; it must agree with
// gitverify's fetchableRemoteURL. ok is false for non-SSH or custom-port remotes.
func sshRemoteHTTPSForm(remote string) (string, bool) {
	if rest, ok := strings.CutPrefix(remote, "ssh://"); ok {
		return sshURLHTTPSForm(rest)
	}
	if strings.Contains(remote, "://") {
		return "", false
	}
	return scpLikeHTTPSForm(remote)
}

// sshURLHTTPSForm refuses an ssh:// remote with its own port.
func sshURLHTTPSForm(rest string) (string, bool) {
	host, path, found := strings.Cut(trimRemoteUser(rest), "/")
	if !found || host == "" || path == "" || strings.ContainsRune(host, ':') {
		return "", false
	}
	return "https://" + host + "/" + path, true
}

// scpLikeHTTPSForm maps git@host:owner/repo.git; a path with a colon is not scp-like.
func scpLikeHTTPSForm(remote string) (string, bool) {
	hostPart, path, found := strings.Cut(remote, ":")
	if !found || path == "" || strings.HasPrefix(path, `\`) {
		return "", false
	}
	host := trimRemoteUser(hostPart)
	if host == "" || strings.ContainsAny(host, `/\`) {
		return "", false
	}
	return "https://" + host + "/" + strings.TrimPrefix(path, "/"), true
}

// trimRemoteUser drops `user@`: the account is not part of the identity.
func trimRemoteUser(hostPart string) string {
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		return hostPart[at+1:]
	}
	return hostPart
}

// lowercasedRemoteSchemeAndHost lowercases scheme and host only, since paths
// may be case-sensitive, and drops any `user@`.
func lowercasedRemoteSchemeAndHost(remote string) string {
	scheme, rest, found := strings.Cut(remote, "://")
	if !found || rest == "" {
		return remote
	}
	authority, path, hasPath := strings.Cut(rest, "/")
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	lowered := strings.ToLower(scheme) + "://" + strings.ToLower(authority)
	if hasPath {
		lowered += "/" + path
	}
	return lowered
}
