// Package gitverify confirms a reported merge actually landed on a tenant's
// real git remote, rather than trusting whoever reports it. It is what lets
// MERGED become a fact about the repository ("this commit is really there,
// with the parent it claims") instead of a claim believed because of its
// caller's identity — see AGENTS.md "Merge Queue".
package gitverify

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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

// fetchBranch clones branch from remoteURL into a fresh in-memory repository,
// so the API needs no persistent checkout of its own for the check.
func fetchBranch(ctx context.Context, remoteURL, branch string) (*git.Repository, error) {
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return nil, err
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	if err != nil {
		return nil, err
	}
	refSpec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch))
	if err := remote.FetchContext(ctx, &git.FetchOptions{RefSpecs: []config.RefSpec{refSpec}}); err != nil {
		return nil, fmt.Errorf("fetching %s from the target remote: %w", branch, err)
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
