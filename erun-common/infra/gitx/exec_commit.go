package gitx

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// CommitWorkingTreeDependencies lets tests replace the git plumbing.
type CommitWorkingTreeDependencies struct {
	CurrentBranch GitValueResolverFunc
	CurrentCommit GitValueResolverFunc
	RunGit        GitCommandRunnerFunc
	StagedFiles   func(core.Context, string) ([]string, error)
	ChangedFiles  func(core.Context, string) ([]string, error)
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
		deps.StagedFiles = GitStagedFiles
	}
	if deps.ChangedFiles == nil {
		deps.ChangedFiles = GitChangedWorkingTreeFiles
	}
	return deps
}

// CommitWorkingTree stages Paths (or everything) and commits Message, refusing a branch
// mismatch or out-of-scope changes before staging.
func CommitWorkingTree(ctx core.Context, root string, params model.CommitWorkingTreeParams, deps CommitWorkingTreeDependencies) (model.CommitWorkingTreeResult, error) {
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("branch is required")
	}
	if strings.TrimSpace(params.Message) == "" {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("message is required")
	}
	deps = normalizeCommitWorkingTreeDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("resolve current branch: %w", err)
	}
	if current != branch {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("refusing to commit: working tree is on branch %q, not the declared %q", current, branch)
	}

	scopedPaths, err := normalizeCommitWorkingTreePaths(root, params.Paths)
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}

	preview, err := resolveCommitPreview(ctx, root, scopedPaths, deps)
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}

	addArgs := commitAddArgs(scopedPaths)
	ctx.TraceCommand(root, "git", append([]string{"add"}, addArgs...)...)
	ctx.TraceCommand(root, "git", CommitVerbArgs(params.SkipHooks, "<message>")...)
	if ctx.DryRun {
		traceCommitPreview(ctx, preview)
		return model.CommitWorkingTreeResult{Branch: branch, Files: preview}, nil
	}

	return stageAndCommitWorkingTree(ctx, root, params.Message, addArgs, params.SkipHooks, deps)
}

// CommitVerbArgs is shared by the dry-run trace and the real invocation.
func CommitVerbArgs(skipHooks bool, message string) []string {
	args := []string{"commit"}
	if skipHooks {
		args = append(args, "--no-verify")
	}
	return append(args, "-m", message)
}

// resolveCommitPreview lists files to commit and enforces the scope; unscoped real
// commits skip it because the post-stage read-back reports what landed.
func resolveCommitPreview(ctx core.Context, root string, scopedPaths []string, deps CommitWorkingTreeDependencies) ([]string, error) {
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

// commitAddArgs prefixes scoped paths with "./" so a leading ":" is not git pathspec
// magic, which would bypass the containment check.
func commitAddArgs(scopedPaths []string) []string {
	if len(scopedPaths) == 0 {
		return []string{"-A"}
	}
	args := make([]string, 0, len(scopedPaths)+1)
	args = append(args, "--")
	for _, path := range scopedPaths {
		args = append(args, "./"+path)
	}
	return args
}

func traceCommitPreview(ctx core.Context, preview []string) {
	if len(preview) == 0 {
		ctx.Trace("commit: no changes to commit")
		return
	}
	ctx.Trace("commit: would commit " + strings.Join(preview, ", "))
}

func stageAndCommitWorkingTree(ctx core.Context, root, message string, addArgs []string, skipHooks bool, deps CommitWorkingTreeDependencies) (model.CommitWorkingTreeResult, error) {
	var addStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &addStderr, append([]string{"add"}, addArgs...)...); err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(addStderr.String()))
	}

	files, err := deps.StagedFiles(ctx, root)
	if err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("resolve staged files: %w", err)
	}
	if len(files) == 0 {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("nothing to commit: the working tree has no changes")
	}

	var commitStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &commitStderr, CommitVerbArgs(skipHooks, message)...); err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("resolve new commit: %w", err)
	}
	// Report the branch git has now, not the declared one.
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.CommitWorkingTreeResult{}, fmt.Errorf("resolve committed branch: %w", err)
	}

	return model.CommitWorkingTreeResult{Branch: landedBranch, Commit: commit, Files: files}, nil
}

// normalizeCommitWorkingTreePaths rejects paths escaping root and returns sorted,
// deduplicated, slash-separated root-relative paths.
func normalizeCommitWorkingTreePaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("path entries must not be blank")
		}
		resolved, canonicalRoot, err := resolveWorkingTreePath(root, trimmed)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(canonicalRoot, resolved)
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

// filesOutsideScope returns changed paths not equal to or nested under a scoped path.
func filesOutsideScope(changed, scoped []string) []string {
	var extra []string
	for _, path := range changed {
		if !pathWithinScope(path, scoped) {
			extra = append(extra, path)
		}
	}
	return extra
}

func pathWithinScope(path string, scoped []string) bool {
	for _, candidate := range scoped {
		if path == candidate || strings.HasPrefix(path, candidate+"/") {
			return true
		}
	}
	return false
}

// GitStagedFiles lists staged paths; -z avoids git's quoting and --relative matches the basis of Paths.
func GitStagedFiles(ctx core.Context, root string) ([]string, error) {
	args := []string{"-C", root, "diff", "--cached", "--relative", "--name-only", "-z"}
	ctx.TraceCommand("", "git", args...)
	output, err := core.Command("git", args...).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
		return nil, err
	}
	return splitNULSeparatedPaths(output), nil
}

// GitChangedWorkingTreeFiles lists staged, unstaged, and untracked paths relative to
// root, which may be below the git top-level.
func GitChangedWorkingTreeFiles(ctx core.Context, root string) ([]string, error) {
	changed := make(map[string]struct{})
	collect := func(args ...string) error {
		traceArgs := append([]string{"-C", root}, args...)
		ctx.TraceCommand("", "git", traceArgs...)
		output, err := core.Command("git", traceArgs...).Output()
		if err != nil {
			if stderr := core.StderrFromExitError(err); stderr != "" {
				return fmt.Errorf("%w: %s", err, stderr)
			}
			return err
		}
		for _, path := range splitNULSeparatedPaths(output) {
			changed[path] = struct{}{}
		}
		return nil
	}

	if err := collect("diff", "--relative", "--name-only", "-z"); err != nil {
		return nil, err
	}
	if err := collect("diff", "--cached", "--relative", "--name-only", "-z"); err != nil {
		return nil, err
	}
	if err := collect("ls-files", "--others", "--exclude-standard", "-z"); err != nil {
		return nil, err
	}

	result := make([]string, 0, len(changed))
	for path := range changed {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

// splitNULSeparatedPaths parses -z output, which unlike the default is never C-quoted.
func splitNULSeparatedPaths(output []byte) []string {
	trimmed := bytes.Trim(output, "\x00")
	if len(trimmed) == 0 {
		return nil
	}
	parts := bytes.Split(trimmed, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, string(part))
	}
	return paths
}
