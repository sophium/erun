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
	// work itself is really there. contained is false, with no error, when
	// the branches are unrelated, share no single merge base, or share no
	// commit carrying the same change set.
	ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (contained bool, commit string, err error)
}

// RemoteVerifier fetches the real remote with go-git, so the API needs no
// `git` binary of its own. It needs no stored credential or configuration
// per tenant either: remoteURL is supplied by the caller reporting the
// merge, the same "origin" its own checkout already pushed to.
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
// squash merge leaves behind, and the only one that matters here: a branch
// that really did land by merge commit or fast-forward is caught by the plain
// ancestor case first, and a branch that adds nothing at all is refused
// rather than treated as a landing.
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
func branchLandedInTarget(repo *git.Repository, targetBranch, sourceBranch string, targetTip, sourceTip *object.Commit) (bool, string, error) {
	// A source tip equal to the target tip is the degenerate case of a
	// branch that is the target; it adds nothing and names no landing.
	if sourceTip.Hash != targetTip.Hash {
		alreadyLanded, err := sourceTip.IsAncestor(targetTip)
		if err != nil {
			return false, "", err
		}
		if alreadyLanded {
			return true, sourceTip.Hash.String(), nil
		}
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
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return nil, err
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	if err != nil {
		return nil, err
	}
	refSpecs := make([]config.RefSpec, 0, len(branches))
	for _, branch := range branches {
		refSpecs = append(refSpecs, config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)))
	}
	if err := remote.FetchContext(ctx, &git.FetchOptions{RefSpecs: refSpecs}); err != nil {
		return nil, fmt.Errorf("fetching %s from the target remote: %w", strings.Join(branches, ", "), err)
	}
	return repo, nil
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
