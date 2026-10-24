package gitx

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

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
func PushWorkingTreeBranch(ctx core.Context, root string, params model.PushWorkingTreeBranchParams, deps PushWorkingTreeBranchDependencies) (model.PushWorkingTreeBranchResult, error) {
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("branch is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizePushWorkingTreeBranchDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("resolve current branch: %w", err)
	}
	if current != branch {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("refusing to push: working tree is on branch %q, not the declared %q", current, branch)
	}

	refspec := branch + ":" + branch
	ctx.TraceCommand(root, "git", "push", remote, refspec)
	if ctx.DryRun {
		return model.PushWorkingTreeBranchResult{Branch: branch, Remote: remote}, nil
	}

	var stderr bytes.Buffer
	pushErr := deps.RunGit(root, io.Discard, &stderr, "push", remote, refspec)
	// stderr is the only place a GitHub ruleset bypass notice appears; report it even
	// when the push failed.
	if notice, ok := parseRulesetBypassNotice(stderr.String()); ok {
		reportRulesetBypass(ctx, remote, notice)
	}
	if pushErr != nil {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("git push: %w: %s", pushErr, strings.TrimSpace(stderr.String()))
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("resolve pushed commit: %w", err)
	}
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("resolve pushed branch: %w", err)
	}

	return model.PushWorkingTreeBranchResult{Branch: landedBranch, Remote: remote, Commit: commit}, nil
}
