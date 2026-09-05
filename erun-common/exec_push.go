package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// PushWorkingTreeBranchParams names the push an orchestrator intends: the
// branch it believes the working tree is on. Branch is verified against the
// tree's actual current branch before anything is pushed, mirroring
// CommitWorkingTree's own branch check — a source branch a review references
// by name is only real once it has actually landed on the remote, so pushing
// whatever HEAD happens to be on instead of the declared branch would be a
// silent, hard-to-notice mismatch.
type PushWorkingTreeBranchParams struct {
	Branch string
	// Remote is the git remote to push to. Empty defaults to "origin".
	Remote string
}

// PushWorkingTreeBranchResult is what actually landed.
type PushWorkingTreeBranchResult struct {
	Branch string `json:"branch"`
	Remote string `json:"remote"`
	Commit string `json:"commit"`
}

// PushWorkingTreeBranchDependencies lets tests replace the git plumbing
// without a real remote, mirroring CommitWorkingTreeDependencies.
type PushWorkingTreeBranchDependencies struct {
	CurrentBranch GitValueResolverFunc
	CurrentCommit GitValueResolverFunc
	RunGit        GitCommandRunnerFunc
}

func normalizePushWorkingTreeBranchDependencies(deps PushWorkingTreeBranchDependencies) PushWorkingTreeBranchDependencies {
	if deps.CurrentBranch == nil {
		deps.CurrentBranch = GitCurrentBranch
	}
	if deps.CurrentCommit == nil {
		deps.CurrentCommit = GitShortCommit
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	return deps
}

// PushWorkingTreeBranch pushes the working tree's current branch to remote.
// Branch is verified against the tree's actual current branch, not assumed —
// a mismatch is refused loudly before anything is pushed, the same discipline
// CommitWorkingTree applies before it stages anything.
//
// This is the primitive erun-common lacked entirely: every push anywhere in
// the codebase before it was either a container image, a helm chart, or the
// release flow's own tag/branch push — never a plain working-tree branch. A
// hosted review references its source branch by name, and the backend can
// only ever fetch what a push actually landed on the remote, so starting a
// review has always needed this operation; "raw" running `git push` was the
// only way to get it, and that is exactly the escape hatch a typed command
// exists to replace.
func PushWorkingTreeBranch(ctx Context, root string, params PushWorkingTreeBranchParams, deps PushWorkingTreeBranchDependencies) (PushWorkingTreeBranchResult, error) {
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("branch is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizePushWorkingTreeBranchDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("resolve current branch: %w", err)
	}
	if current != branch {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("refusing to push: working tree is on branch %q, not the declared %q", current, branch)
	}

	refspec := branch + ":" + branch
	ctx.TraceCommand(root, "git", "push", remote, refspec)
	if ctx.DryRun {
		return PushWorkingTreeBranchResult{Branch: branch, Remote: remote}, nil
	}

	var stderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &stderr, "push", remote, refspec); err != nil {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("git push: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	// Read back the commit and branch actually pushed rather than trusting the
	// pre-push state, the same read-after-write discipline CommitWorkingTree
	// applies to its own result.
	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("resolve pushed commit: %w", err)
	}
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("resolve pushed branch: %w", err)
	}

	return PushWorkingTreeBranchResult{Branch: landedBranch, Remote: remote, Commit: commit}, nil
}
