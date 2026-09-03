package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// MergeWorkingTreeBranchParams names the branch to bring into the working
// tree's current branch. Comments anchor to commitId + filePath + line, so a
// rewrite (rebase) would orphan every thread on a review; this always merges.
type MergeWorkingTreeBranchParams struct {
	// TargetBranch is the branch to merge in, resolved against Remote (e.g.
	// TargetBranch "main" with the default Remote merges "origin/main").
	TargetBranch string
	// Remote is the git remote TargetBranch is fetched from. Empty defaults to
	// "origin".
	Remote string
}

// MergeWorkingTreeBranchResult is what actually landed.
type MergeWorkingTreeBranchResult struct {
	Branch       string `json:"branch"`
	TargetBranch string `json:"targetBranch"`
	Remote       string `json:"remote"`
	Commit       string `json:"commit"`
}

// MergeConflictError reports a merge that left the working tree mid-conflict
// — a distinct, named outcome rather than a generic failure, since the
// worktree is left half-merged and the caller (an operator or an agent) must
// resolve the conflicted files and commit, or run `git merge --abort`,
// before doing anything else with it.
type MergeConflictError struct {
	TargetBranch    string
	ConflictedFiles []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf(
		"merging %s left %d file(s) conflicted: %s — resolve them and commit, or run `git merge --abort` to back out",
		e.TargetBranch, len(e.ConflictedFiles), strings.Join(e.ConflictedFiles, ", "),
	)
}

// MergeWorkingTreeBranchDependencies lets tests replace the git plumbing
// without a real remote, mirroring PushWorkingTreeBranchDependencies.
type MergeWorkingTreeBranchDependencies struct {
	CurrentBranch   GitValueResolverFunc
	CurrentCommit   GitValueResolverFunc
	RunGit          GitCommandRunnerFunc
	ConflictedFiles func(root string, runGit GitCommandRunnerFunc) ([]string, error)
	// RunAtlasHash regenerates a conflicted atlas.sum instead of leaving it
	// for a human to resolve. See resolveAtlasSumConflicts.
	RunAtlasHash AtlasMigrateHashRunnerFunc
}

func normalizeMergeWorkingTreeBranchDependencies(deps MergeWorkingTreeBranchDependencies) MergeWorkingTreeBranchDependencies {
	if deps.CurrentBranch == nil {
		deps.CurrentBranch = GitCurrentBranch
	}
	if deps.CurrentCommit == nil {
		deps.CurrentCommit = GitShortCommit
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	if deps.ConflictedFiles == nil {
		deps.ConflictedFiles = gitMergeConflictedFiles
	}
	if deps.RunAtlasHash == nil {
		deps.RunAtlasHash = runAtlasMigrateHash
	}
	return deps
}

// MergeWorkingTreeBranch fetches Remote/TargetBranch and merges it into the
// working tree's current branch with an explicit merge commit — never a
// rebase. A conflicted merge is reported as *MergeConflictError, distinct
// from any other failure, and the worktree is left exactly as git left it
// (mid-merge) for the caller to resolve or abort; nothing here cleans that up
// automatically.
func MergeWorkingTreeBranch(ctx Context, root string, params MergeWorkingTreeBranchParams, deps MergeWorkingTreeBranchDependencies) (MergeWorkingTreeBranchResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("target branch is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeMergeWorkingTreeBranchDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve current branch: %w", err)
	}

	remoteRef := remote + "/" + target
	ctx.TraceCommand(root, "git", "fetch", remote, target)
	ctx.TraceCommand(root, "git", "merge", "--no-edit", remoteRef)
	if ctx.DryRun {
		return MergeWorkingTreeBranchResult{Branch: current, TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndMergeWorkingTree(ctx, root, target, remote, remoteRef, deps)
}

// fetchAndMergeWorkingTree runs the mutating half of MergeWorkingTreeBranch,
// isolated so the validation and dry-run branching above it don't inflate
// that function's complexity.
func fetchAndMergeWorkingTree(ctx Context, root, target, remote, remoteRef string, deps MergeWorkingTreeBranchDependencies) (MergeWorkingTreeBranchResult, error) {
	var fetchStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &fetchStderr, "fetch", remote, target); err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(fetchStderr.String()))
	}

	var mergeStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &mergeStderr, "merge", "--no-edit", remoteRef); err != nil {
		conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
		if conflictErr != nil || len(conflicted) == 0 {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("git merge: %w: %s", err, strings.TrimSpace(mergeStderr.String()))
		}
		remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
		if resolveErr != nil {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("regenerate conflicted atlas.sum: %w", resolveErr)
		}
		if len(remaining) > 0 {
			return MergeWorkingTreeBranchResult{}, &MergeConflictError{TargetBranch: target, ConflictedFiles: remaining}
		}
		// Every conflict was an atlas.sum this regenerated and staged —
		// finish the merge commit `git merge` left pending.
		var commitStderr bytes.Buffer
		if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "--no-edit"); err != nil {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("git commit --no-edit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
		}
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged commit: %w", err)
	}
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged branch: %w", err)
	}

	return MergeWorkingTreeBranchResult{Branch: landedBranch, TargetBranch: target, Remote: remote, Commit: commit}, nil
}

// gitMergeConflictedFiles lists the paths a failed merge left conflicted, so
// MergeWorkingTreeBranch can tell a real conflict apart from any other merge
// failure (a bad ref, a network error) by what git actually left behind
// rather than by guessing from exit codes or stderr text.
func gitMergeConflictedFiles(root string, runGit GitCommandRunnerFunc) ([]string, error) {
	var stdout, stderr bytes.Buffer
	if err := runGit(root, &stdout, &stderr, "diff", "--name-only", "--diff-filter=U"); err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=U: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
