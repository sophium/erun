package eruncommon

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// CommitWorkingTreeParams names the commit an orchestrator intends: the
// branch it believes the working tree is on, and the message to record.
// Neither is defaulted or assumed — Branch is verified against the tree's
// actual current branch before anything is staged.
type CommitWorkingTreeParams struct {
	Branch string
	// Message is recorded verbatim as the commit message. It is data, not a
	// shell fragment: nothing in it is interpreted, expanded, or executed.
	Message string
	// Paths scopes the commit to a known set of paths (relative to root, or
	// absolute inside it) instead of every change in the tree. When set, the
	// commit stages only these paths and is refused if the tree has any
	// other changes outside them — an unrelated writer's edits can then
	// never be absorbed into a commit the caller did not ask for. Empty
	// keeps the unscoped `git add -A` behavior.
	Paths []string
}

// CommitWorkingTreeResult is what actually landed.
type CommitWorkingTreeResult struct {
	Branch string   `json:"branch"`
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
}

// CommitWorkingTreeDependencies lets tests replace the git plumbing without a
// real repository, mirroring the release flow's dependency-injection pattern.
type CommitWorkingTreeDependencies struct {
	CurrentBranch GitValueResolverFunc
	CurrentCommit GitValueResolverFunc
	RunGit        GitCommandRunnerFunc
	StagedFiles   func(Context, string) ([]string, error)
	ChangedFiles  func(Context, string) ([]string, error)
}

func normalizeCommitWorkingTreeDependencies(deps CommitWorkingTreeDependencies) CommitWorkingTreeDependencies {
	if deps.CurrentBranch == nil {
		deps.CurrentBranch = GitCurrentBranch
	}
	if deps.CurrentCommit == nil {
		deps.CurrentCommit = GitShortCommit
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	if deps.StagedFiles == nil {
		deps.StagedFiles = gitStagedFiles
	}
	if deps.ChangedFiles == nil {
		deps.ChangedFiles = gitChangedWorkingTreeFiles
	}
	return deps
}

// CommitWorkingTree stages Paths (or every change in root when Paths is
// empty) and commits with Message. Branch is verified against the tree's
// actual current branch, not assumed — a mismatch is refused loudly before
// anything is staged, rather than landing the commit on whichever branch HEAD
// happens to be on. When Paths is set, the commit is refused just as loudly
// if the tree has changes outside the declared paths, rather than silently
// leaving them out of the commit.
func CommitWorkingTree(ctx Context, root string, params CommitWorkingTreeParams, deps CommitWorkingTreeDependencies) (CommitWorkingTreeResult, error) {
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		return CommitWorkingTreeResult{}, fmt.Errorf("branch is required")
	}
	if strings.TrimSpace(params.Message) == "" {
		return CommitWorkingTreeResult{}, fmt.Errorf("message is required")
	}
	deps = normalizeCommitWorkingTreeDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve current branch: %w", err)
	}
	if current != branch {
		return CommitWorkingTreeResult{}, fmt.Errorf("refusing to commit: working tree is on branch %q, not the declared %q", current, branch)
	}

	scopedPaths, err := normalizeCommitWorkingTreePaths(root, params.Paths)
	if err != nil {
		return CommitWorkingTreeResult{}, err
	}

	preview, err := resolveCommitPreview(ctx, root, scopedPaths, deps)
	if err != nil {
		return CommitWorkingTreeResult{}, err
	}

	addArgs := commitAddArgs(scopedPaths)
	ctx.TraceCommand(root, "git", append([]string{"add"}, addArgs...)...)
	ctx.TraceCommand(root, "git", "commit", "-m", "<message>")
	if ctx.DryRun {
		traceCommitPreview(ctx, preview)
		return CommitWorkingTreeResult{Branch: branch, Files: preview}, nil
	}

	return stageAndCommitWorkingTree(ctx, root, branch, params.Message, addArgs, deps)
}

// resolveCommitPreview reports the files a commit would include, and — when
// scopedPaths is set — refuses before anything is staged if the tree has
// changes outside them. The read only runs when it is load-bearing: a scoped
// commit needs it to enforce the refusal, and a dry-run needs it so the
// preview names files rather than just the git verbs. An unscoped real commit
// skips it — the post-stage read-back in stageAndCommitWorkingTree already
// reports what actually landed.
func resolveCommitPreview(ctx Context, root string, scopedPaths []string, deps CommitWorkingTreeDependencies) ([]string, error) {
	if len(scopedPaths) == 0 && !ctx.DryRun {
		return nil, nil
	}
	changed, err := deps.ChangedFiles(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("resolve changed files: %w", err)
	}
	if len(scopedPaths) > 0 {
		if extra := filesOutsideScope(changed, scopedPaths); len(extra) > 0 {
			return nil, fmt.Errorf("refusing to commit: the working tree has changes outside the declared paths: %s", strings.Join(extra, ", "))
		}
	}
	return changed, nil
}

// commitAddArgs is the `git add` argv for the resolved scope: every change,
// or only the declared paths.
func commitAddArgs(scopedPaths []string) []string {
	if len(scopedPaths) == 0 {
		return []string{"-A"}
	}
	return append([]string{"--"}, scopedPaths...)
}

// traceCommitPreview names, in a dry run, exactly what a real run would
// commit — the property the preview previously lacked.
func traceCommitPreview(ctx Context, preview []string) {
	if len(preview) == 0 {
		ctx.Trace("commit: no changes to commit")
		return
	}
	ctx.Trace("commit: would commit " + strings.Join(preview, ", "))
}

// stageAndCommitWorkingTree runs the mutating half of CommitWorkingTree,
// isolated so the branch-verification and dry-run branching above it don't
// inflate that function's complexity.
func stageAndCommitWorkingTree(ctx Context, root, branch, message string, addArgs []string, deps CommitWorkingTreeDependencies) (CommitWorkingTreeResult, error) {
	if err := deps.RunGit(root, io.Discard, io.Discard, append([]string{"add"}, addArgs...)...); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git add: %w", err)
	}

	files, err := deps.StagedFiles(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve staged files: %w", err)
	}
	if len(files) == 0 {
		return CommitWorkingTreeResult{}, fmt.Errorf("nothing to commit: the working tree has no changes")
	}

	if err := deps.RunGit(root, io.Discard, io.Discard, "commit", "-m", message); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git commit: %w", err)
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve new commit: %w", err)
	}

	return CommitWorkingTreeResult{Branch: branch, Commit: commit, Files: files}, nil
}

// normalizeCommitWorkingTreePaths validates and canonicalizes caller-declared
// paths against root, reusing the same escape check as WriteWorkingTreeFile
// so a scoped commit can never be pointed outside the working tree. Returns
// sorted, deduplicated, slash-separated paths relative to root, matching the
// form git itself reports them in.
func normalizeCommitWorkingTreePaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		resolved, err := resolveWorkingTreePath(root, trimmed)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(filepath.Clean(root), resolved)
		if err != nil {
			return nil, fmt.Errorf("path %q is outside the working tree %q", trimmed, root)
		}
		relative = filepath.ToSlash(relative)
		if _, ok := seen[relative]; ok {
			continue
		}
		seen[relative] = struct{}{}
		normalized = append(normalized, relative)
	}
	sort.Strings(normalized)
	return normalized, nil
}

// filesOutsideScope returns the entries of changed that are not present in
// scoped, sorted as changed already is.
func filesOutsideScope(changed, scoped []string) []string {
	allowed := make(map[string]struct{}, len(scoped))
	for _, path := range scoped {
		allowed[path] = struct{}{}
	}
	var extra []string
	for _, path := range changed {
		if _, ok := allowed[path]; !ok {
			extra = append(extra, path)
		}
	}
	return extra
}

// gitStagedFiles lists what is about to be committed, read back after staging
// rather than assumed, so Files always reflects what git actually staged.
func gitStagedFiles(ctx Context, root string) ([]string, error) {
	ctx.TraceCommand("", "git", "-C", root, "diff", "--cached", "--name-only")
	output, err := Command("git", "-C", root, "diff", "--cached", "--name-only").Output()
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// gitChangedWorkingTreeFiles lists every path with an uncommitted change —
// staged, unstaged, or untracked — so a scoped commit can be checked against
// the caller's declared paths, and an unscoped dry-run can name what would be
// committed, before anything is staged.
func gitChangedWorkingTreeFiles(ctx Context, root string) ([]string, error) {
	changed := make(map[string]struct{})
	collect := func(args ...string) error {
		traceArgs := append([]string{"-C", root}, args...)
		ctx.TraceCommand("", "git", traceArgs...)
		output, err := Command("git", traceArgs...).Output()
		if err != nil {
			return err
		}
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return nil
		}
		for _, path := range strings.Split(trimmed, "\n") {
			if path = strings.TrimSpace(path); path != "" {
				changed[path] = struct{}{}
			}
		}
		return nil
	}

	if err := collect("diff", "--name-only"); err != nil {
		return nil, err
	}
	if err := collect("diff", "--cached", "--name-only"); err != nil {
		return nil, err
	}
	if err := collect("ls-files", "--others", "--exclude-standard"); err != nil {
		return nil, err
	}

	result := make([]string, 0, len(changed))
	for path := range changed {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}
