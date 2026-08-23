package eruncommon

import (
	"bytes"
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
	// keeps the unscoped `git add -A` behavior. A blank entry is refused
	// rather than dropped, since a caller-declared scope that resolves to
	// nothing is far more likely a bug upstream than an intentional no-op,
	// and silently falling back to "commit everything" is exactly the
	// failure this scoping exists to prevent.
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

	return stageAndCommitWorkingTree(ctx, root, params.Message, addArgs, deps)
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
// or only the declared paths. Each scoped path is prefixed with "./" so a
// path that happens to start with ":" is never reinterpreted as git pathspec
// magic (":/foo" means "foo relative to the top of the working tree",
// regardless of what filesystem path the string otherwise looks like) — the
// filesystem-level containment check in normalizeCommitWorkingTreePaths would
// otherwise be silently bypassed by git resolving the very same string
// against a different, unbounded root.
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
func stageAndCommitWorkingTree(ctx Context, root, message string, addArgs []string, deps CommitWorkingTreeDependencies) (CommitWorkingTreeResult, error) {
	var addStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &addStderr, append([]string{"add"}, addArgs...)...); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(addStderr.String()))
	}

	files, err := deps.StagedFiles(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve staged files: %w", err)
	}
	if len(files) == 0 {
		return CommitWorkingTreeResult{}, fmt.Errorf("nothing to commit: the working tree has no changes")
	}

	var commitStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "-m", message); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve new commit: %w", err)
	}
	// Read back the branch the commit actually landed on rather than trusting
	// the declared one: the pre-stage check already refused a mismatch, but
	// reporting what git verified up front instead of what git has now is the
	// same read-after-write discipline the commit id and Files already follow.
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve committed branch: %w", err)
	}

	return CommitWorkingTreeResult{Branch: landedBranch, Commit: commit, Files: files}, nil
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

// filesOutsideScope returns the entries of changed that are not present in,
// or nested under, any of scoped — so a caller can scope a commit to a
// directory and not have every file inside it reported as "outside" the very
// scope that names their parent.
func filesOutsideScope(changed, scoped []string) []string {
	var extra []string
	for _, path := range changed {
		if !pathWithinScope(path, scoped) {
			extra = append(extra, path)
		}
	}
	return extra
}

// pathWithinScope reports whether path is exactly one of scoped, or lives
// underneath a directory named in scoped.
func pathWithinScope(path string, scoped []string) bool {
	for _, candidate := range scoped {
		if path == candidate || strings.HasPrefix(path, candidate+"/") {
			return true
		}
	}
	return false
}

// gitStagedFiles lists what is about to be committed, read back after staging
// rather than assumed, so Files always reflects what git actually staged.
// NUL-separated output (-z) sidesteps git's default quoting of non-ASCII and
// otherwise-special path bytes, and --relative reports paths relative to root
// rather than the top of the enclosing repository, matching the basis Paths
// is declared and compared in.
func gitStagedFiles(ctx Context, root string) ([]string, error) {
	args := []string{"-C", root, "diff", "--cached", "--relative", "--name-only", "-z"}
	ctx.TraceCommand("", "git", args...)
	output, err := Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	return splitNULSeparatedPaths(output), nil
}

// gitChangedWorkingTreeFiles lists every path with an uncommitted change —
// staged, unstaged, or untracked — so a scoped commit can be checked against
// the caller's declared paths, and an unscoped dry-run can name what would be
// committed, before anything is staged. All three reads are scoped to root
// via --relative (ls-files is already root-relative by default) so a runtime
// repo root that is not the git top-level still gets a guard that reasons
// about root's own subtree, in the same basis Paths is declared in — without
// it, changes elsewhere in the enclosing repository either never surface (the
// guard is blind to them) or never match the declared paths (every scoped
// commit is refused).
func gitChangedWorkingTreeFiles(ctx Context, root string) ([]string, error) {
	changed := make(map[string]struct{})
	collect := func(args ...string) error {
		traceArgs := append([]string{"-C", root}, args...)
		ctx.TraceCommand("", "git", traceArgs...)
		output, err := Command("git", traceArgs...).Output()
		if err != nil {
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

// splitNULSeparatedPaths parses the output of a git invocation run with -z,
// which separates entries with NUL and never quotes them — unlike the
// newline-separated default, which C-quotes non-ASCII and other special path
// bytes and so cannot round-trip every valid filename.
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
