// Package gitverify confirms a reported merge actually landed on a tenant's
// real git remote, rather than trusting whoever reports it. It is what lets
// MERGED become a fact about the repository ("this commit is really there,
// with the parent it claims") instead of a claim believed because of its
// caller's identity — see AGENTS.md "Merge Queue".
package gitverify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Verifier answers whether a commit is really reachable from a branch's tip
// on a remote, and what that commit's own parent is.
type Verifier interface {
	// Contains fetches remoteURL and reports whether commit is on branch (its
	// own tip counts), together with commit's parent commit hash (empty for a
	// root commit). ok is false, with no error, when the fetch succeeds but
	// commit is not reachable from branch's tip at all.
	Contains(ctx context.Context, remoteURL, branch, commit string) (ok bool, parent string, err error)

	// IsAncestor fetches remoteURL and reports whether ancestor is descendant's
	// own ancestor, or the same commit — never whether it is the *immediate*
	// parent. This tolerates commits landing on branch between ancestor and
	// descendant (a release push, for instance) without treating them as
	// evidence the merge was built on the wrong base. isAncestor is false,
	// with no error, when the fetch succeeds but ancestor is not reachable
	// from descendant at all.
	IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (isAncestor bool, err error)

	// ContainsChanges fetches remoteURL and reports whether everything
	// sourceBranch adds, relative to where it diverged from targetBranch, is
	// already present in targetBranch's history — naming the commit on
	// targetBranch that carries it. This is the question commit identity
	// cannot answer once a branch lands by squash merge: the branch's own
	// commits are deliberately not made ancestors of the target, so
	// IsAncestor and a gate build both have nothing to point at, while the
	// work itself is really there. A source tip that is already in the
	// target's history — its own tip included — is answered by that ancestry
	// before any change set is compared. contained is false, with no error,
	// when the branches are unrelated, share no single merge base, or share
	// no commit carrying the same change set.
	ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (contained bool, commit string, err error)
}

// RemoteVerifier fetches the real remote with go-git, so the API needs no
// `git` binary of its own. It needs no stored credential or configuration
// per tenant either: remoteURL is supplied by the caller reporting the
// merge, the same "origin" its own checkout already pushed to.
//
// It never authenticates, so a remote URL in a form that needs an identity to
// read is rewritten by fetchableRemoteURL below into one this process can
// read anonymously before the fetch — the caller naming its own repository's
// remote must not depend on this runtime holding a key to it.
type RemoteVerifier struct{}

func NewRemoteVerifier() *RemoteVerifier { return &RemoteVerifier{} }

func (RemoteVerifier) Contains(ctx context.Context, remoteURL, branch, commit string) (bool, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	branch = strings.TrimSpace(branch)
	commit = strings.TrimSpace(commit)
	if remoteURL == "" || branch == "" || commit == "" {
		return false, "", fmt.Errorf("remoteURL, branch, and commit are all required")
	}
	if !plumbing.IsHash(commit) {
		return false, "", fmt.Errorf("commit %q is not a git commit hash", commit)
	}

	repo, err := fetchBranch(ctx, remoteURL, branch)
	if err != nil {
		return false, "", err
	}
	tipCommit, err := branchTip(repo, branch)
	if err != nil {
		return false, "", err
	}

	targetCommit, err := repo.CommitObject(plumbing.NewHash(commit))
	if err != nil {
		// Not present anywhere in the fetched history: definitely not on the
		// branch, not a lookup failure.
		return false, "", nil
	}
	return commitReachesTip(targetCommit, tipCommit)
}

// normalizeAncestorArgs trims and validates IsAncestor's arguments, kept
// separate so IsAncestor's own branching stays about the git question, not
// input hygiene.
func normalizeAncestorArgs(remoteURL, branch, ancestor, descendant string) (string, string, string, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	branch = strings.TrimSpace(branch)
	ancestor = strings.TrimSpace(ancestor)
	descendant = strings.TrimSpace(descendant)
	if remoteURL == "" || branch == "" || ancestor == "" || descendant == "" {
		return "", "", "", "", fmt.Errorf("remoteURL, branch, ancestor, and descendant are all required")
	}
	if !plumbing.IsHash(ancestor) {
		return "", "", "", "", fmt.Errorf("commit %q is not a git commit hash", ancestor)
	}
	if !plumbing.IsHash(descendant) {
		return "", "", "", "", fmt.Errorf("commit %q is not a git commit hash", descendant)
	}
	return remoteURL, branch, ancestor, descendant, nil
}

func (RemoteVerifier) IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (bool, error) {
	remoteURL, branch, ancestor, descendant, err := normalizeAncestorArgs(remoteURL, branch, ancestor, descendant)
	if err != nil {
		return false, err
	}
	if ancestor == descendant {
		return true, nil
	}

	repo, err := fetchBranch(ctx, remoteURL, branch)
	if err != nil {
		return false, err
	}
	ancestorCommit, err := repo.CommitObject(plumbing.NewHash(ancestor))
	if err != nil {
		// Not present anywhere in the fetched history: definitely not an
		// ancestor, not a lookup failure.
		return false, nil
	}
	descendantCommit, err := repo.CommitObject(plumbing.NewHash(descendant))
	if err != nil {
		return false, err
	}
	return ancestorCommit.IsAncestor(descendantCommit)
}

// ContainsChanges answers whether sourceBranch's work is already in
// targetBranch even though the branch's own commits are not — the shape a
// squash merge leaves behind. A branch that landed by merge commit or
// fast-forward — including one the target was fast-forwarded *onto*, where the
// two tips are the same commit — is caught by the plain ancestor case first,
// before any change set is compared.
func (RemoteVerifier) ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (bool, string, error) {
	remoteURL, targetBranch, sourceBranch, err := normalizeChangeArgs(remoteURL, targetBranch, sourceBranch)
	if err != nil {
		return false, "", err
	}

	repo, err := fetchBranches(ctx, remoteURL, targetBranch, sourceBranch)
	if err != nil {
		return false, "", err
	}
	targetTip, err := branchTip(repo, targetBranch)
	if err != nil {
		return false, "", err
	}
	sourceTip, err := branchTip(repo, sourceBranch)
	if err != nil {
		return false, "", err
	}
	return branchLandedInTarget(repo, targetBranch, sourceBranch, targetTip, sourceTip)
}

// normalizeChangeArgs trims and validates ContainsChanges' arguments, kept
// separate so its own branching stays about the git question, not input
// hygiene — the same split normalizeAncestorArgs makes for IsAncestor.
func normalizeChangeArgs(remoteURL, targetBranch, sourceBranch string) (string, string, string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	targetBranch = strings.TrimSpace(targetBranch)
	sourceBranch = strings.TrimSpace(sourceBranch)
	if remoteURL == "" || targetBranch == "" || sourceBranch == "" {
		return "", "", "", fmt.Errorf("remoteURL, targetBranch, and sourceBranch are all required")
	}
	if targetBranch == sourceBranch {
		return "", "", "", fmt.Errorf("targetBranch and sourceBranch are both %q", targetBranch)
	}
	return remoteURL, targetBranch, sourceBranch, nil
}

// branchLandedInTarget is the two ways a branch's work can be in the target
// while its own commits are not the whole story: the ordinary landing, where
// the source tip really is in the target's history, and the squash landing,
// where only the work is.
//
// A source tip equal to the target tip belongs to the first. It is the shape
// a fast-forward of the target onto the branch leaves behind, which is a
// sanctioned way for a change to land without this platform's queue, and a
// commit is its own ancestor (IsAncestor's own contract) — so the branch is
// contained in the target by identity. It is also the one shape the refs
// cannot tell apart from a branch that never committed anything, and the
// change-set comparison cannot separate them either: both leave an empty
// fingerprint, and a branch that genuinely adds nothing is already answered as
// contained whenever it trails the target rather than sitting exactly on it.
// Refusing the equal-tip case would therefore buy no protection against a
// landing that did not happen while refusing the landing that did.
func branchLandedInTarget(repo *git.Repository, targetBranch, sourceBranch string, targetTip, sourceTip *object.Commit) (bool, string, error) {
	alreadyLanded, err := sourceTip.IsAncestor(targetTip)
	if err != nil {
		return false, "", err
	}
	if alreadyLanded {
		return true, sourceTip.Hash.String(), nil
	}

	wanted, base, err := branchChangeFingerprint(targetBranch, sourceBranch, targetTip, sourceTip)
	if err != nil || wanted == "" {
		return false, "", err
	}
	landed, err := findSquashedLanding(repo, targetTip, base, wanted)
	if err != nil || landed == "" {
		return false, "", err
	}
	return true, landed, nil
}

// branchChangeFingerprint is what the branch adds relative to where it
// diverged, together with that merge base. An empty fingerprint is no
// evidence of landing: it means the branches share no single merge base, so
// there is no "what this branch added" to compare against, or that the branch
// adds nothing at all.
func branchChangeFingerprint(targetBranch, sourceBranch string, targetTip, sourceTip *object.Commit) (string, *object.Commit, error) {
	bases, err := sourceTip.MergeBase(targetTip)
	if err != nil {
		return "", nil, fmt.Errorf("finding the merge base of %s and %s: %w", sourceBranch, targetBranch, err)
	}
	if len(bases) != 1 {
		return "", nil, nil
	}
	wanted, err := changeFingerprint(bases[0], sourceTip)
	if err != nil {
		return "", nil, err
	}
	return wanted, bases[0], nil
}

// findSquashedLanding walks the target's own history for a commit whose
// change against its parent is exactly the branch's change set, and names it.
func findSquashedLanding(repo *git.Repository, targetTip, base *object.Commit, wanted string) (string, error) {
	iter, err := repo.Log(&git.LogOptions{From: targetTip.Hash})
	if err != nil {
		return "", err
	}
	landed := ""
	err = iter.ForEach(func(candidate *object.Commit) error {
		carries, err := commitCarriesChange(candidate, base, wanted)
		if err != nil {
			return err
		}
		if !carries {
			return nil
		}
		landed = candidate.Hash.String()
		return storer.ErrStop
	})
	if err != nil && !errors.Is(err, storer.ErrStop) {
		return "", err
	}
	return landed, nil
}

// commitCarriesChange reports whether candidate's own change against its
// parent is the wanted one. Commits at or before the merge base are shared
// history rather than the branch's work, and a merge commit has no single
// "its own change" to compare — a squash commit always has one parent, so
// skipping them costs nothing and avoids matching on an ambiguous diff.
func commitCarriesChange(candidate, base *object.Commit, wanted string) (bool, error) {
	if candidate.Hash == base.Hash || len(candidate.ParentHashes) != 1 {
		return false, nil
	}
	atOrBeforeBase, err := candidate.IsAncestor(base)
	if err != nil {
		return false, err
	}
	if atOrBeforeBase {
		return false, nil
	}
	parent, err := candidate.Parent(0)
	if err != nil {
		return false, err
	}
	fingerprint, err := changeFingerprint(parent, candidate)
	if err != nil {
		return false, err
	}
	return fingerprint == wanted, nil
}

// changeFingerprint is a stable identity for the set of changes between two
// commits' trees: every added, removed, and modified path with the blob it
// moved to or from. Two fingerprints matching means the same content changed
// in the same way, whatever graph produced it — which is what lets a squash
// commit be recognized as a branch's work when none of the branch's commits
// are ancestors of the target. An empty fingerprint means the two commits
// hold the same tree.
func changeFingerprint(from, to *object.Commit) (string, error) {
	fromTree, err := from.Tree()
	if err != nil {
		return "", err
	}
	toTree, err := to.Tree()
	if err != nil {
		return "", err
	}
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s %s %s %s",
			action,
			change.From.Name, change.From.TreeEntry.Mode, change.From.TreeEntry.Hash,
			change.To.Name, change.To.TreeEntry.Mode, change.To.TreeEntry.Hash))
	}
	// Sorted, so the fingerprint identifies the change set and not the order
	// the diff happened to emit it in.
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// fetchBranch clones branch from remoteURL into a fresh in-memory repository,
// so the API needs no persistent checkout of its own for the check.
func fetchBranch(ctx context.Context, remoteURL, branch string) (*git.Repository, error) {
	return fetchBranches(ctx, remoteURL, branch)
}

// fetchBranches clones each branch from remoteURL into one fresh in-memory
// repository, so two branches' histories can be compared without a second
// fetch of the same remote.
func fetchBranches(ctx context.Context, remoteURL string, branches ...string) (*git.Repository, error) {
	fetchURL, err := fetchableRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return nil, err
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{fetchURL}})
	if err != nil {
		return nil, err
	}
	refSpecs := make([]config.RefSpec, 0, len(branches))
	for _, branch := range branches {
		refSpecs = append(refSpecs, config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)))
	}
	if err := remote.FetchContext(ctx, &git.FetchOptions{RefSpecs: refSpecs}); err != nil {
		return nil, fmt.Errorf("fetching %s from %s: %w. %s", strings.Join(branches, ", "), fetchURL, err,
			unreadableRemoteNote(remoteURL, fetchURL))
	}
	return repo, nil
}

// fetchableRemoteURL rewrites a caller's remote URL into the form this process
// can fetch on its own, and is what keeps `git remote get-url origin` — an
// SSH remote on most checkouts — a usable --remote-url. The verifier only ever
// reads the target remote and holds no key or agent, so an SSH remote is
// answered over its host's credential-less HTTPS instead; a public repository
// is readable that way without anyone's credentials. A URL that is already
// readable anonymously (https, http, git, file, a local path) is returned
// untouched, so a caller who named a private remote keeps the form they chose
// and the fetch failure stays about their URL rather than one we substituted.
//
// An SSH remote on a port of its own has no HTTPS equivalent to carry it to,
// so it is refused here, naming the form the platform needs, instead of being
// sent to a fetch that would fail with an SSH-agent error this runtime cannot
// act on.
func fetchableRemoteURL(remoteURL string) (string, error) {
	given := strings.TrimSpace(remoteURL)
	if given == "" {
		return "", fmt.Errorf("remoteURL is required")
	}
	if rest, ok := strings.CutPrefix(given, "ssh://"); ok {
		return sshRemoteAsHTTPS(remoteURL, rest)
	}
	// Any other scheme is either already credential-less (https, http, git,
	// file) or not one of git's, and is left for the fetch to judge.
	if strings.Contains(given, "://") {
		return given, nil
	}
	return scpLikeRemoteAsHTTPS(given), nil
}

// sshRemoteAsHTTPS answers an ssh:// remote with the same host's HTTPS form,
// refusing the one shape that has none to be carried to: a remote on a port of
// its own.
func sshRemoteAsHTTPS(remoteURL, rest string) (string, error) {
	host, path, found := strings.Cut(trimUser(rest), "/")
	if !found || host == "" || path == "" {
		return "", fmt.Errorf("remote-url %q names neither an ssh host nor a repository path", remoteURL)
	}
	if strings.ContainsRune(host, ':') {
		return "", fmt.Errorf("remote-url %q is an ssh remote on a port of its own, which has no HTTPS equivalent: pass the repository's HTTPS URL, because the platform fetches the target remote without credentials", remoteURL)
	}
	return "https://" + host + "/" + path, nil
}

// scpLikeRemoteAsHTTPS answers git@host:owner/repo.git with the same host's
// HTTPS form. A local path with a colon in it (or a Windows drive) is not
// scp-like — its host part carries a separator — and is returned unchanged.
func scpLikeRemoteAsHTTPS(given string) string {
	hostPart, path, found := strings.Cut(given, ":")
	if !found || path == "" || strings.HasPrefix(path, `\`) {
		return given
	}
	host := trimUser(hostPart)
	if host == "" || strings.ContainsAny(host, `/\`) {
		return given
	}
	return "https://" + host + "/" + strings.TrimPrefix(path, "/")
}

// trimUser drops the `user@` an SSH remote may carry; the platform reads
// anonymously, so the identity it names is not one to fetch as.
func trimUser(hostPart string) string {
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		return hostPart[at+1:]
	}
	return hostPart
}

// unreadableRemoteNote is appended to a verification fetch that failed. By the
// time this fetch runs the caller's push has already landed, so the refusal
// has to say what it is about — the platform's own read of the target remote —
// rather than read as a verdict that the merge did not happen. Where the URL
// was one we rewrote, it names both forms, because the caller is holding a
// remote this platform could not read as given.
func unreadableRemoteNote(remoteURL, fetchURL string) string {
	if strings.TrimSpace(remoteURL) != fetchURL {
		return fmt.Sprintf("the platform reads the target remote over HTTPS without credentials, so remote-url %q was fetched as %s; whether the merge landed was not judged by this failure",
			strings.TrimSpace(remoteURL), fetchURL)
	}
	return "the platform fetches the target remote without credentials; whether the merge landed was not judged by this failure"
}

// branchTip reads the commit at branch's just-fetched tip.
func branchTip(repo *git.Repository, branch string) (*object.Commit, error) {
	tipRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return nil, fmt.Errorf("reading the fetched tip of %s: %w", branch, err)
	}
	tipCommit, err := repo.CommitObject(tipRef.Hash())
	if err != nil {
		return nil, err
	}
	return tipCommit, nil
}

// commitReachesTip reports whether target is tip itself or one of its
// ancestors, together with target's own parent commit hash.
func commitReachesTip(target, tip *object.Commit) (bool, string, error) {
	parent := ""
	if len(target.ParentHashes) > 0 {
		parent = target.ParentHashes[0].String()
	}
	if target.Hash == tip.Hash {
		return true, parent, nil
	}
	isAncestor, err := target.IsAncestor(tip)
	if err != nil {
		return false, "", err
	}
	return isAncestor, parent, nil
}
