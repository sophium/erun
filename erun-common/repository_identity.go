package eruncommon

import (
	"fmt"
	"strings"
)

// repository_identity.go answers "which repository is this?" from a git
// remote, so a review can record the repository its branches belong to rather
// than naming it only by convention. A tenant may serve more than one
// repository, and two of them sharing a target branch would otherwise share
// one merge queue with no way to tell whose queued review is whose.

// RepositoryIdentity canonicalizes a git remote into the identity of the
// repository it names. Two spellings of one repository — an SSH checkout's
// `git@github.com:owner/repo.git` and the HTTPS remote the platform reads —
// answer the same identity, so a review created from one is found by a caller
// holding the other. That equality is the whole point: without it a developer
// on an SSH checkout and the environment driving the merge queue would each
// see an empty queue for the other's review.
//
// The identity is a remote spelling, not necessarily a fetchable URL: an SSH
// remote on a port of its own has no HTTPS form and keeps its own spelling.
// Whether the platform can read a given remote is gitverify's question, asked
// separately when a merge is reported.
func RepositoryIdentity(remote string) (string, error) {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return "", fmt.Errorf("a repository remote is required: pass the repository's own git remote, e.g. the output of `git remote get-url origin`")
	}
	canonical := trimmed
	if httpsForm, ok := sshRemoteHTTPSForm(trimmed); ok {
		canonical = httpsForm
	}
	// `.git` and a trailing slash are two spellings of the same repository,
	// and git hands back either depending on how the remote was configured.
	canonical = strings.TrimSuffix(strings.TrimRight(canonical, "/"), ".git")
	canonical = strings.TrimRight(canonical, "/")
	canonical = lowercasedRemoteSchemeAndHost(canonical)
	// A bare host names a forge, not a repository on it. Refused rather than
	// stored, because every review for every repository that forge hosts
	// would otherwise answer to one identity. A file remote legitimately
	// carries no authority, so only its path has to be there.
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

// ResolveReviewRepository answers which repository a review belongs to: the
// remote the caller named, or the checkout they are standing in when they
// named none. A command that always runs against the repository the operator
// is in should not make them retype its remote to be usable at all.
//
// It is an error, not an empty answer, when neither names a repository: a
// review whose repository cannot be recorded cannot be placed in any
// repository's merge queue, and creating one anyway is the dead end this
// resolution exists to prevent.
func ResolveReviewRepository(ctx Context, remoteURL, traceLabel string) (string, error) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		ctx.Trace(traceLabel + ": no repository given; reading origin from the current checkout")
		origin, err := originRemoteURL()
		if err != nil {
			ctx.Trace(traceLabel + ": this checkout has no origin remote")
			return "", fmt.Errorf("a repository is required to open a review: pass --repository with the repository's remote, or run this from a clone of it")
		}
		remote = origin
		ctx.Trace(traceLabel + ": origin = " + remote)
	}
	identity, err := RepositoryIdentity(remote)
	if err != nil {
		ctx.Trace(traceLabel + ": repository resolution failed: " + err.Error())
		return "", err
	}
	ctx.Trace(traceLabel + ": repository = " + identity)
	return identity, nil
}

// sshRemoteHTTPSForm answers the HTTPS remote for the same repository when
// remote is an SSH one, and ok=false when it is not an SSH remote at all or is
// an ssh:// remote on a port of its own, which has no HTTPS form to be named
// by. gitverify's fetchableRemoteURL performs the same rewrite for a different
// question — what this process can fetch without credentials — and the two
// must agree on the mapping, or a review created over SSH would not be found
// by the HTTPS remote its merge is verified against.
func sshRemoteHTTPSForm(remote string) (string, bool) {
	if rest, ok := strings.CutPrefix(remote, "ssh://"); ok {
		return sshURLHTTPSForm(rest)
	}
	// Any other scheme is not SSH, and is its own identity as given.
	if strings.Contains(remote, "://") {
		return "", false
	}
	return scpLikeHTTPSForm(remote)
}

// sshURLHTTPSForm answers the HTTPS form of an ssh:// remote's own body,
// refusing the shape with none to be carried to: one on a port of its own.
func sshURLHTTPSForm(rest string) (string, bool) {
	host, path, found := strings.Cut(trimRemoteUser(rest), "/")
	if !found || host == "" || path == "" || strings.ContainsRune(host, ':') {
		return "", false
	}
	return "https://" + host + "/" + path, true
}

// scpLikeHTTPSForm answers the HTTPS form of git@host:owner/repo.git. A local
// path with a colon in it (or a Windows drive) is not scp-like: its host part
// carries a separator, so it names a path rather than a host.
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

// trimRemoteUser drops the `user@` an SSH remote may carry; the identity is
// the repository, not the account someone reaches it as. Two developers
// cloning over different accounts share one repository.
func trimRemoteUser(hostPart string) string {
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		return hostPart[at+1:]
	}
	return hostPart
}

// lowercasedRemoteSchemeAndHost folds the case-insensitive half of a remote —
// a host is not case-sensitive, and neither is a scheme — while leaving the
// path alone, since a path is only case-insensitive on some hosts and
// lowercasing it would merge two repositories that really are distinct. Any
// `user@` is dropped for the same reason the ssh rewrite drops it: the
// account someone reaches a repository as is not part of which repository it
// is, and two developers cloning over different accounts must agree.
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
