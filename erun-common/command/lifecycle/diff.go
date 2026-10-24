package lifecycle

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

type DiffResult struct {
	WorkingDirectory string         `json:"workingDirectory,omitempty"`
	RawDiff          string         `json:"rawDiff"`
	Summary          DiffSummary    `json:"summary"`
	Files            []DiffFile     `json:"files,omitempty"`
	Tree             []DiffTreeNode `json:"tree,omitempty"`
	ReviewBase       DiffReviewBase `json:"reviewBase,omitempty"`
	ReviewCommits    []DiffCommit   `json:"reviewCommits,omitempty"`
	Scope            string         `json:"scope,omitempty"`
	SelectedCommit   string         `json:"selectedCommit,omitempty"`
	IncludesWorktree bool           `json:"includesWorktree,omitempty"`
}

type DiffSummary struct {
	FileCount int `json:"fileCount"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type DiffFile struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	NewPath   string     `json:"newPath,omitempty"`
	Status    string     `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Binary    bool       `json:"binary,omitempty"`
	Hunks     []DiffHunk `json:"hunks,omitempty"`
}

type DiffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines,omitempty"`
}

type DiffLine struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

type DiffTreeNode struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	ParentPath string `json:"parentPath,omitempty"`
	Type       string `json:"type"`
	Depth      int    `json:"depth"`
	Status     string `json:"status,omitempty"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
}

type DiffReviewBase struct {
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit,omitempty"`
	ShortCommit string `json:"shortCommit,omitempty"`
}

type DiffCommit struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Subject   string `json:"subject"`
	Author    string `json:"author"`
	Date      string `json:"date"`
}

type DiffOptions struct {
	Scope          string `json:"scope,omitempty"`
	SelectedCommit string `json:"selectedCommit,omitempty"`
	// Target is "env" (default, the runtime working directory) or "erun" (the contribute-mode clone).
	Target string `json:"target,omitempty"`
}

const (
	DiffTargetEnv  = "env"
	DiffTargetERun = "erun"
)

// NormalizeDiffTarget canonicalizes the diff target, defaulting to "env".
func NormalizeDiffTarget(target string) string {
	switch strings.TrimSpace(target) {
	case DiffTargetERun:
		return DiffTargetERun
	default:
		return DiffTargetEnv
	}
}

// ResolveDiffTargetRoot returns the working directory to diff for target.
func ResolveDiffTargetRoot(target, envRoot, homeDir string) (string, error) {
	switch NormalizeDiffTarget(target) {
	case DiffTargetERun:
		return core.ContributeClonePath(homeDir)
	default:
		return strings.TrimSpace(envRoot), nil
	}
}

func ResolveGitDiff(projectRoot string, runGit gitx.GitCommandRunnerFunc) (DiffResult, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return DiffResult{}, fmt.Errorf("project root is required")
	}
	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, currentScopeGitDiffArgs(gitDiffHeadFound(projectRoot, runGit))...); err != nil {
		return DiffResult{}, fmt.Errorf("git diff: %w%s", err, core.FormatGitCommandStderr(stderr.String()))
	}
	if err := appendUntrackedGitDiff(projectRoot, stdout, runGit); err != nil {
		return DiffResult{}, err
	}

	result := ParseGitDiff(stdout.String())
	result.WorkingDirectory = projectRoot
	result.IncludesWorktree = true
	return result, nil
}

func ResolveGitDiffWithOptions(projectRoot string, options DiffOptions, runGit gitx.GitCommandRunnerFunc) (DiffResult, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return DiffResult{}, fmt.Errorf("project root is required")
	}
	if runGit == nil {
		runGit = gitx.GitCommandRunner
	}

	scope := normalizeDiffScope(options.Scope)
	selectedCommit := strings.TrimSpace(options.SelectedCommit)
	if err := rejectOptionShapedSelectedCommit(scope, selectedCommit); err != nil {
		return DiffResult{}, err
	}

	base, baseFound, err := resolveGitDiffReviewBase(projectRoot, runGit)
	if err != nil {
		return DiffResult{}, err
	}
	commits, err := resolveGitDiffReviewCommits(projectRoot, base, baseFound, runGit)
	if err != nil {
		return DiffResult{}, err
	}

	selectedCommit, err = resolveDiffSelectedCommit(projectRoot, scope, selectedCommit, runGit)
	if err != nil {
		return DiffResult{}, err
	}

	stdout := new(bytes.Buffer)
	diffArgs := gitDiffReviewArgs(base.Commit, baseFound, scope, selectedCommit, gitDiffHeadFound(projectRoot, runGit))
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, diffArgs...); err != nil {
		return DiffResult{}, fmt.Errorf("git diff: %w%s", err, core.FormatGitCommandStderr(stderr.String()))
	}
	if err := appendUntrackedGitDiff(projectRoot, stdout, runGit); err != nil {
		return DiffResult{}, err
	}

	result := ParseGitDiff(stdout.String())
	result.WorkingDirectory = projectRoot
	result.ReviewBase = base
	result.ReviewCommits = commits
	result.Scope = scope
	result.SelectedCommit = selectedCommit
	result.IncludesWorktree = true
	return result, nil
}

func normalizeDiffScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "all", "commit":
		return strings.TrimSpace(scope)
	default:
		return "current"
	}
}

// exec_diff is read-scoped, so a caller-supplied revision must never parse as a git option.
func rejectOptionShapedSelectedCommit(scope, selectedCommit string) error {
	if scope != "commit" || selectedCommit == "" {
		return nil
	}
	if strings.HasPrefix(selectedCommit, "-") {
		return fmt.Errorf("selectedCommit must be a commit revision, not %q", selectedCommit)
	}
	return nil
}

// Resolving through git ensures only a real commit sha reaches the diff argv.
func resolveDiffSelectedCommit(projectRoot, scope, selectedCommit string, runGit gitx.GitCommandRunnerFunc) (string, error) {
	if scope != "commit" || selectedCommit == "" {
		return selectedCommit, nil
	}
	resolved, err := gitOutput(projectRoot, runGit, "rev-parse", "--verify", selectedCommit+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("selectedCommit %q is not a valid commit: %w", selectedCommit, err)
	}
	return resolved, nil
}

func gitDiffReviewArgs(baseCommit string, baseFound bool, scope, selectedCommit string, headFound bool) []string {
	switch scope {
	case "all":
		args := []string{"diff", "--no-color", "--no-ext-diff"}
		if baseFound {
			args = append(args, baseCommit)
		}
		return args
	case "commit":
		args := []string{"diff", "--no-color", "--no-ext-diff"}
		if selectedCommit != "" {
			args = append(args, selectedCommit+"^")
		}
		return args
	default:
		// Include staged changes: a bare `git diff` hides them.
		return currentScopeGitDiffArgs(headFound)
	}
}

const gitEmptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Without a HEAD, diff against the empty tree so freshly staged files still show.
func currentScopeGitDiffArgs(headFound bool) []string {
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if headFound {
		return append(args, "HEAD")
	}
	return append(args, gitEmptyTreeHash)
}

func gitDiffHeadFound(projectRoot string, runGit gitx.GitCommandRunnerFunc) bool {
	_, err := gitOutput(projectRoot, runGit, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

func resolveGitDiffReviewBase(projectRoot string, runGit gitx.GitCommandRunnerFunc) (DiffReviewBase, bool, error) {
	var selected DiffReviewBase
	selectedDistance := -1
	for _, branch := range []string{"origin/HEAD", "origin/main", "origin/develop", "main", "develop"} {
		commit, err := gitOutput(projectRoot, runGit, "merge-base", "HEAD", branch)
		if err != nil || commit == "" {
			continue
		}
		distance, err := gitOutput(projectRoot, runGit, "rev-list", "--count", commit+"..HEAD")
		if err != nil {
			continue
		}
		parsedDistance, err := strconv.Atoi(strings.TrimSpace(distance))
		if err != nil {
			continue
		}
		shortCommit, _ := gitOutput(projectRoot, runGit, "rev-parse", "--short", commit)
		displayBranch := resolveGitDiffReviewBaseBranch(projectRoot, branch, runGit)
		if selectedDistance < 0 || parsedDistance < selectedDistance {
			selectedDistance = parsedDistance
			selected = DiffReviewBase{Branch: displayBranch, Commit: commit, ShortCommit: shortCommit}
		}
	}
	if selected.Commit != "" {
		return selected, true, nil
	}
	return DiffReviewBase{}, false, nil
}

func resolveGitDiffReviewBaseBranch(projectRoot, branch string, runGit gitx.GitCommandRunnerFunc) string {
	if branch != "origin/HEAD" {
		return branch
	}
	resolved, err := gitOutput(projectRoot, runGit, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil || resolved == "" {
		return branch
	}
	return resolved
}

func resolveGitDiffReviewCommits(projectRoot string, base DiffReviewBase, baseFound bool, runGit gitx.GitCommandRunnerFunc) ([]DiffCommit, error) {
	if !baseFound {
		return nil, nil
	}
	output, err := gitOutput(projectRoot, runGit, "log", "--reverse", "--date=iso-strict", "--pretty=format:%H%x1f%h%x1f%an%x1f%aI%x1f%s%x1e", base.Commit+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseGitDiffReviewCommits(output), nil
}

func gitOutput(projectRoot string, runGit gitx.GitCommandRunnerFunc, args ...string) (string, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, args...); err != nil {
		return "", fmt.Errorf("%w%s", err, core.FormatGitCommandStderr(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func parseGitDiffReviewCommits(output string) []DiffCommit {
	output = strings.TrimSuffix(output, "\x1e")
	if output == "" {
		return nil
	}
	records := strings.Split(output, "\x1e")
	commits := make([]DiffCommit, 0, len(records))
	for _, record := range records {
		fields := strings.SplitN(strings.TrimSpace(record), "\x1f", 5)
		if len(fields) != 5 {
			continue
		}
		commits = append(commits, DiffCommit{
			Hash:      fields[0],
			ShortHash: fields[1],
			Author:    fields[2],
			Date:      fields[3],
			Subject:   fields[4],
		})
	}
	return commits
}

func appendUntrackedGitDiff(projectRoot string, rawDiff *bytes.Buffer, runGit gitx.GitCommandRunnerFunc) error {
	untrackedOutput := new(bytes.Buffer)
	untrackedStderr := new(bytes.Buffer)
	if err := runGit(projectRoot, untrackedOutput, untrackedStderr, "ls-files", "--others", "--exclude-standard", "-z"); err != nil {
		return fmt.Errorf("git ls-files: %w%s", err, core.FormatGitCommandStderr(untrackedStderr.String()))
	}
	for _, file := range splitNULTerminated(untrackedOutput.String()) {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		fileDiff := new(bytes.Buffer)
		fileStderr := new(bytes.Buffer)
		err := runGit(projectRoot, fileDiff, fileStderr, "diff", "--no-color", "--no-ext-diff", "--no-index", "--", "/dev/null", file)
		if err != nil && fileDiff.Len() == 0 {
			return fmt.Errorf("git diff untracked %s: %w%s", file, err, core.FormatGitCommandStderr(fileStderr.String()))
		}
		appendRawDiff(rawDiff, fileDiff.String())
	}
	return nil
}

func splitNULTerminated(value string) []string {
	value = strings.TrimSuffix(value, "\x00")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\x00")
}

func appendRawDiff(rawDiff *bytes.Buffer, diff string) {
	if diff == "" {
		return
	}
	if rawDiff.Len() > 0 && !strings.HasSuffix(rawDiff.String(), "\n") {
		rawDiff.WriteString("\n")
	}
	rawDiff.WriteString(diff)
	if !strings.HasSuffix(diff, "\n") {
		rawDiff.WriteString("\n")
	}
}

func WriteRawDiff(stdout io.Writer, result DiffResult) error {
	if result.RawDiff == "" {
		return nil
	}
	_, err := io.WriteString(stdout, result.RawDiff)
	return err
}
