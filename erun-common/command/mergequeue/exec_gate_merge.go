package mergequeue

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// GateMergeSource is one branch to squash, in landing order; Message is its squash
// commit message, with the branch's carried trailers appended beneath it.
type GateMergeSource struct {
	Branch  string
	Message string
}

// GateMergeWorkingTreeParams squash-merges Sources onto TargetBranch's current remote tip,
// one commit per landed source. Batching tests whether branches build together.
type GateMergeWorkingTreeParams struct {
	Sources []GateMergeSource
	// TargetBranch is checked out fresh from its remote tip, not the working tree's branch.
	TargetBranch string
	// Remote defaults to "origin".
	Remote string
	// UnderLeaseID names an exclusive environment claim the caller already holds.
	UnderLeaseID string
}

// GateMergeLandedSource is one source branch that squash-merged cleanly.
type GateMergeLandedSource struct {
	SourceBranch string `json:"sourceBranch"`
	SourceCommit string `json:"sourceCommit"`
	Commit       string `json:"commit"`
	// CarriedTrailers is always serialized, so "carried none" is distinguishable from "not read".
	CarriedTrailers []string `json:"carriedTrailers"`
}

// GateMergeSkippedSource is one source that did not land; a skip is reported, not fatal.
type GateMergeSkippedSource struct {
	SourceBranch    string   `json:"sourceBranch"`
	SourceCommit    string   `json:"sourceCommit,omitempty"`
	Reason          string   `json:"reason"`
	ConflictedFiles []string `json:"conflictedFiles,omitempty"`
}

// GateMergeWorkingTreeResult is what landed; Commit is the stack tip, or the target's tip if
// every source was skipped.
type GateMergeWorkingTreeResult struct {
	TargetBranch string                   `json:"targetBranch"`
	Remote       string                   `json:"remote"`
	Commit       string                   `json:"commit"`
	Landed       []GateMergeLandedSource  `json:"landed"`
	Skipped      []GateMergeSkippedSource `json:"skipped,omitempty"`
}

// GateMergeWorkingTreeDependencies lets tests replace the git plumbing.
type GateMergeWorkingTreeDependencies struct {
	WorkingTreeClean func(ctx core.Context, root string) (bool, error)
	ResolveRef       func(ctx core.Context, root, ref string) (string, error)
	RunGit           gitx.GitCommandRunnerFunc
	ConflictedFiles  func(root string, runGit gitx.GitCommandRunnerFunc) ([]string, error)
	StagedPaths      func(ctx core.Context, root string) ([]string, error)
	RunAtlasHash     AtlasMigrateHashRunnerFunc
}

func normalizeGateMergeWorkingTreeDependencies(deps GateMergeWorkingTreeDependencies) GateMergeWorkingTreeDependencies {
	if deps.WorkingTreeClean == nil {
		deps.WorkingTreeClean = shared.GitWorktreeClean
	}
	if deps.ResolveRef == nil {
		deps.ResolveRef = gitResolveRef
	}
	if deps.RunGit == nil {
		deps.RunGit = gitx.GitCommandRunner
	}
	if deps.ConflictedFiles == nil {
		deps.ConflictedFiles = gitMergeConflictedFiles
	}
	if deps.StagedPaths == nil {
		deps.StagedPaths = gitx.GitStagedFiles
	}
	if deps.RunAtlasHash == nil {
		deps.RunAtlasHash = runAtlasMigrateHash
	}
	return deps
}

// GateMergeWorkingTree squashes each source onto a fresh checkout of TargetBranch. A source
// that conflicts (other than only atlas.sum), stages nothing, or fails the regression
// policy is skipped, not fatal. The working tree must be clean, since this switches branches.
func GateMergeWorkingTree(ctx core.Context, root string, params GateMergeWorkingTreeParams, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("target branch is required")
	}
	if err := validateGateMergeSources(params.Sources); err != nil {
		return GateMergeWorkingTreeResult{}, err
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeGateMergeWorkingTreeDependencies(deps)

	// Gate-merges rewrite the environment's one shared worktree, so concurrent runs land the
	// wrong tree; refuse before fetching, in --dry-run too.
	if err := shared.EnsureEnvironmentNotExclusivelyHeld(ctx, "gate-merge", params.UnderLeaseID); err != nil {
		return GateMergeWorkingTreeResult{}, err
	}

	clean, err := deps.WorkingTreeClean(ctx, root)
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("check working tree is clean: %w", err)
	}
	if !clean {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("refusing to gate-merge: the working tree has uncommitted changes")
	}

	targetRef := gateMergeFetchRef(target)
	fetchArgs := traceGateMergePlan(ctx, root, params.Sources, target, remote, targetRef)
	if ctx.DryRun {
		return GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndGateMergeWorkingTree(ctx, root, params.Sources, target, remote, fetchArgs, targetRef, deps)
}

func validateGateMergeSources(sources []GateMergeSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("at least one source branch is required")
	}
	for _, source := range sources {
		if strings.TrimSpace(source.Branch) == "" {
			return fmt.Errorf("source branch is required for every entry")
		}
		if strings.TrimSpace(source.Message) == "" {
			return fmt.Errorf("message is required for every source branch")
		}
	}
	return nil
}

// gateMergeCarriedTrailerLines is the closed set of load-bearing trailers a squash carries
// onto the target; other trailers stay with the branch's history.
var gateMergeCarriedTrailerLines = []gateMergeCarriedTrailerLine{
	{pattern: regexp.MustCompile(`^Closes[ \t]+#[0-9]+(?:[ \t]*,[ \t]*#[0-9]+)*$`), singleLine: true},
	{pattern: regexp.MustCompile(`^Reproduces:[ \t]+\S.*$`)},
	{pattern: regexp.MustCompile(`^Regression-Test:[ \t]+\S.*$`)},
	{pattern: regexp.MustCompile(`^Regression-Test-Existing:[ \t]+\S.*$`)},
	{pattern: regexp.MustCompile(`^Regression-Test-Exemption:[ \t]+\S.*$`)},
	{pattern: regexp.MustCompile(`^Defect-Fix:[ \t]+(?i:yes|no|true|false)$`), singleLine: true},
}

type gateMergeCarriedTrailerLine struct {
	pattern *regexp.Regexp
	// singleLine marks a spelling whose value cannot continue onto the next line.
	singleLine bool
}

func gateMergeCarriedTrailer(line string) *gateMergeCarriedTrailerLine {
	for i := range gateMergeCarriedTrailerLines {
		if gateMergeCarriedTrailerLines[i].pattern.MatchString(line) {
			return &gateMergeCarriedTrailerLines[i]
		}
	}
	return nil
}

var gateMergeTrailerToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:`)

// gateMergeReferencedIssue matches "Token #N"; an uncarried reference still ends the entry above it.
var gateMergeReferencedIssue = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*[ \t]+#[0-9]+`)

// gateMergeStartsTrailerEntry is deliberately narrower than "any token-shaped line": bare
// "Token value" matches ordinary prose and would truncate hard-wrapped values.
func gateMergeStartsTrailerEntry(line string) bool {
	return gateMergeTrailerToken.MatchString(line) || gateMergeReferencedIssue.MatchString(line)
}

// The base..source range excludes trailers of already-landed branches; NUL separates bodies.
func gateMergeTrailerLogArgs(base, sourceRef string) []string {
	return []string{"log", "--format=%B%x00", base + ".." + sourceRef}
}

// gateMergeCarriedTrailers fails on a git error rather than returning empty, since a missing
// trailer is otherwise invisible on the target.
func gateMergeCarriedTrailers(ctx core.Context, root, base, sourceRef string, deps GateMergeWorkingTreeDependencies) ([]string, []string, error) {
	args := gateMergeTrailerLogArgs(base, sourceRef)
	ctx.TraceCommand(root, "git", args...)
	output, err := runGitCapturingOutput(root, deps, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read the trailers on %s: %w", sourceRef, err)
	}
	bodies := strings.Split(output, "\x00")
	var trailers []string
	for _, body := range bodies {
		trailers = append(trailers, gateMergeTrailersFromBody(body)...)
	}
	return trailers, bodies, nil
}

// gateMergeTrailersFromBody considers every line of the body: any "trailer block" boundary
// has silently dropped declarations. The narrow carried set decides carriage.
func gateMergeTrailersFromBody(body string) []string {
	return gateMergeTrailerEntries(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
}

// gateMergeTrailerEntries folds continuation lines (any non-blank line that does not start a
// new entry) into the entry; a blank line ends it. singleLine spellings open no entry.
func gateMergeTrailerEntries(lines []string) []string {
	var carried []string
	var entry []string
	flush := func() {
		if len(entry) > 0 {
			carried = append(carried, strings.Join(entry, "\n"))
			entry = nil
		}
	}
	for _, line := range lines {
		if gateMergeStartsTrailerEntry(line) {
			flush()
			if trailer := gateMergeCarriedTrailer(line); trailer != nil {
				if trailer.singleLine {
					carried = append(carried, line)
					continue
				}
				entry = []string{line}
			}
			continue
		}
		if len(entry) > 0 && gateMergeContinuesTrailer(line) {
			entry = append(entry, line)
			continue
		}
		flush()
	}
	flush()
	return carried
}

func gateMergeContinuesTrailer(line string) bool {
	return strings.TrimSpace(line) != ""
}

// gateMergeCommitMessage appends the carried trailers beneath message, each once.
func gateMergeCommitMessage(message string, trailers []string) string {
	var carried []string
	seen := make(map[string]bool, len(trailers))
	for _, trailer := range trailers {
		if seen[trailer] || strings.Contains(message, trailer) {
			continue
		}
		seen[trailer] = true
		carried = append(carried, trailer)
	}
	if len(carried) == 0 {
		return message
	}
	return strings.TrimRight(message, "\n") + "\n\n" + strings.Join(carried, "\n")
}

// gateMergeFetchRef stages fetched branches under refs/erun/, since a remote-tracking name
// does not exist when --remote is a URL.
func gateMergeFetchRef(branch string) string {
	return "refs/erun/gate-merge/" + branch
}

// The "+" forces the update so a reused environment never gates against a stale tip; the
// source stays unqualified so tags still resolve.
func gateMergeFetchSpec(branch string) string {
	return "+" + branch + ":" + gateMergeFetchRef(branch)
}

// traceGateMergePlan traces the whole plan, also outside --dry-run, and returns the fetch argv.
func traceGateMergePlan(ctx core.Context, root string, sources []GateMergeSource, target, remote, targetRef string) []string {
	fetchArgs := []string{"fetch", remote, gateMergeFetchSpec(target)}
	for _, source := range sources {
		fetchArgs = append(fetchArgs, gateMergeFetchSpec(source.Branch))
	}
	ctx.TraceCommand(root, "git", fetchArgs...)
	ctx.TraceCommand(root, "git", "checkout", "-B", target, targetRef)
	for _, source := range sources {
		ctx.TraceCommand(root, "git", "merge", "--squash", gateMergeFetchRef(source.Branch))
		ctx.TraceCommand(root, "git", gateMergeTrailerLogArgs(targetRef, gateMergeFetchRef(source.Branch))...)
		ctx.TraceCommand(root, "git", gitx.CommitVerbArgs(true, "<message + the branch trailers>")...)
	}
	return fetchArgs
}

func fetchAndGateMergeWorkingTree(ctx core.Context, root string, sources []GateMergeSource, target, remote string, fetchArgs []string, targetRef string, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	if output, err := runGitCapturingOutput(root, deps, fetchArgs...); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git fetch: %w: %s", err, output)
	}

	if output, err := runGitCapturingOutput(root, deps, "checkout", "-B", target, targetRef); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git checkout: %w: %s", err, output)
	}

	result := GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}
	tip, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("resolve target tip: %w", err)
	}
	result.Commit = tip

	for _, source := range sources {
		landed, skipped, err := gateMergeOneSource(ctx, root, source, remote, targetRef, deps)
		if err != nil {
			return GateMergeWorkingTreeResult{}, err
		}
		if skipped != nil {
			result.Skipped = append(result.Skipped, *skipped)
			continue
		}
		result.Landed = append(result.Landed, *landed)
		result.Commit = landed.Commit
	}

	return result, nil
}

// runGitCapturingOutput collects both streams: git reports some failures, e.g. "nothing to
// commit", on stdout. os/exec drains identical writers through one pipe.
func runGitCapturingOutput(root string, deps GateMergeWorkingTreeDependencies, args ...string) (string, error) {
	var output bytes.Buffer
	err := deps.RunGit(root, &output, &output, args...)
	return strings.TrimSpace(output.String()), err
}

// gateMergeOneSource squashes and commits one source. Conflicts are backed out with
// `reset --hard` (--squash records no MERGE_HEAD to abort) and skipped; other git failures are fatal.
func gateMergeOneSource(ctx core.Context, root string, source GateMergeSource, remote, targetRef string, deps GateMergeWorkingTreeDependencies) (*GateMergeLandedSource, *GateMergeSkippedSource, error) {
	sourceRef := gateMergeFetchRef(source.Branch)
	sourceCommit, err := deps.ResolveRef(ctx, root, sourceRef)
	if err != nil {
		return nil, &GateMergeSkippedSource{
			SourceBranch: source.Branch,
			Reason:       fmt.Sprintf("could not resolve %s after fetch: %v", sourceRef, err),
		}, nil
	}

	if mergeOutput, err := runGitCapturingOutput(root, deps, "merge", "--squash", sourceRef); err != nil {
		skipped, skip, skipErr := skipConflictedGateMergeSource(root, source, remote, sourceCommit, sourceRef, mergeOutput, err, deps)
		if skipErr != nil {
			return nil, nil, skipErr
		}
		if skip {
			return nil, &skipped, nil
		}
		// Every conflict was a regenerated atlas.sum; land like a clean squash.
	}

	// A squash that staged nothing means the source is already on the target: skip, don't commit.
	staged, err := deps.StagedPaths(ctx, root)
	if err != nil {
		return nil, nil, fmt.Errorf("read what squashing %s staged: %w", source.Branch, err)
	}
	if len(staged) == 0 {
		return nil, &GateMergeSkippedSource{
			SourceBranch: source.Branch,
			SourceCommit: sourceCommit,
			Reason:       fmt.Sprintf("squashing %s onto %s staged no changes: the source is already contained in the target", source.Branch, remote),
		}, nil
	}

	return commitGateMergeSource(ctx, root, source, sourceCommit, sourceRef, targetRef, staged, deps)
}

func commitGateMergeSource(ctx core.Context, root string, source GateMergeSource, sourceCommit, sourceRef, targetRef string, staged []string, deps GateMergeWorkingTreeDependencies) (*GateMergeLandedSource, *GateMergeSkippedSource, error) {
	trailers, bodies, err := gateMergeCarriedTrailers(ctx, root, targetRef, sourceRef, deps)
	if err != nil {
		return nil, nil, err
	}
	skipped, refused, err := refuseGateMergeSourceOnPolicy(root, source, sourceCommit, bodies, staged, deps)
	if err != nil {
		return nil, nil, err
	}
	if refused {
		return nil, &skipped, nil
	}
	// Skip hooks: the source was already built, and a hook under another toolchain can
	// rewrite the tree.
	if output, err := runGitCapturingOutput(root, deps, gitx.CommitVerbArgs(true, gateMergeCommitMessage(source.Message, trailers))...); err != nil {
		return nil, nil, fmt.Errorf("git commit %s: %w: %s", source.Branch, err, output)
	}

	commit, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve squash merge commit for %s: %w", source.Branch, err)
	}

	return &GateMergeLandedSource{
		SourceBranch:    source.Branch,
		SourceCommit:    sourceCommit,
		Commit:          commit,
		CarriedTrailers: trailers,
	}, nil, nil
}

// refuseGateMergeSourceOnPolicy backs out and skips a source that fails the regression policy.
func refuseGateMergeSourceOnPolicy(root string, source GateMergeSource, sourceCommit string, bodies, staged []string, deps GateMergeWorkingTreeDependencies) (GateMergeSkippedSource, bool, error) {
	readFile := func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) }
	policyErr := evaluateMergeRegressionPolicy(source.Branch, bodies, staged, readFile)
	if policyErr == nil {
		return GateMergeSkippedSource{}, false, nil
	}
	var resetStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
		return GateMergeSkippedSource{}, false, fmt.Errorf("git reset --hard after refusing %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
	}
	return GateMergeSkippedSource{
		SourceBranch: source.Branch,
		SourceCommit: sourceCommit,
		Reason:       "regression-test policy: " + policyErr.Error(),
	}, true, nil
}

// skipConflictedGateMergeSource returns skip=false with nil error when every conflict was a
// regenerated atlas.sum; a non-nil error is fatal for the batch.
func skipConflictedGateMergeSource(root string, source GateMergeSource, remote, sourceCommit, sourceRef, mergeOutput string, mergeErr error, deps GateMergeWorkingTreeDependencies) (GateMergeSkippedSource, bool, error) {
	conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
	if conflictErr != nil || len(conflicted) == 0 {
		return GateMergeSkippedSource{}, false, fmt.Errorf("git merge --squash %s: %w: %s", sourceRef, mergeErr, mergeOutput)
	}
	remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
	if resolveErr != nil {
		return GateMergeSkippedSource{}, false, fmt.Errorf("regenerate conflicted atlas.sum for %s: %w", source.Branch, resolveErr)
	}
	if len(remaining) == 0 {
		return GateMergeSkippedSource{}, false, nil
	}
	var resetStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
		return GateMergeSkippedSource{}, false, fmt.Errorf("git reset --hard after a conflicted squash of %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
	}
	return GateMergeSkippedSource{
		SourceBranch:    source.Branch,
		SourceCommit:    sourceCommit,
		Reason:          fmt.Sprintf("squashing %s onto %s left %d file(s) conflicted", source.Branch, remote, len(remaining)),
		ConflictedFiles: remaining,
	}, true, nil
}

func gitResolveRef(ctx core.Context, root, ref string) (string, error) {
	ctx.TraceCommand("", "git", "-C", root, "rev-parse", ref)
	output, err := core.Command("git", "-C", root, "rev-parse", ref).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
