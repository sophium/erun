package gitx

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// PushWorkingTreeBranchParams names the intended branch; it is verified before pushing.
type PushWorkingTreeBranchParams struct {
	Branch string
	// Remote defaults to "origin".
	Remote string
}

// PushWorkingTreeBranchResult is what actually landed.
type PushWorkingTreeBranchResult struct {
	Branch string `json:"branch"`
	Remote string `json:"remote"`
	Commit string `json:"commit"`
}

// PushWorkingTreeBranchDependencies lets tests replace the git plumbing.
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

// PushWorkingTreeBranch pushes the current branch to remote, refusing a branch mismatch first.
func PushWorkingTreeBranch(ctx core.Context, root string, params PushWorkingTreeBranchParams, deps PushWorkingTreeBranchDependencies) (PushWorkingTreeBranchResult, error) {
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
	pushErr := deps.RunGit(root, io.Discard, &stderr, "push", remote, refspec)
	// stderr is the only place a GitHub ruleset bypass notice appears; report it even
	// when the push failed.
	if notice, ok := parseRulesetBypassNotice(stderr.String()); ok {
		reportRulesetBypass(ctx, remote, notice)
	}
	if pushErr != nil {
		return PushWorkingTreeBranchResult{}, fmt.Errorf("git push: %w: %s", pushErr, strings.TrimSpace(stderr.String()))
	}

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
