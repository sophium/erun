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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared/gitref"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
)

// GateMergeWorkingTreeDependencies lets tests replace the git plumbing.
type GateMergeWorkingTreeDependencies struct {
	WorkingTreeClean func(ctx core.Context, root string) (bool, error)
	ResolveRef       func(ctx core.Context, root, ref string) (string, error)
	RunGit           seam.GitCommandRunnerFunc
	ConflictedFiles  func(root string, runGit seam.GitCommandRunnerFunc) ([]string, error)
	StagedPaths      func(ctx core.Context, root string) ([]string, error)
	RunAtlasHash     AtlasMigrateHashRunnerFunc
}

func normalizeGateMergeWorkingTreeDependencies(deps GateMergeWorkingTreeDependencies) GateMergeWorkingTreeDependencies {
	if deps.WorkingTreeClean == nil {
		deps.WorkingTreeClean = gitref.GitWorktreeClean
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
func GateMergeWorkingTree(ctx core.Context, root string, params model.GateMergeWorkingTreeParams, deps GateMergeWorkingTreeDependencies) (model.GateMergeWorkingTreeResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("target branch is required")
	}
	if err := validateGateMergeSources(params.Sources); err != nil {
		return model.GateMergeWorkingTreeResult{}, err
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeGateMergeWorkingTreeDependencies(deps)

	// Gate-merges rewrite the environment's one shared worktree, so concurrent runs land the
	// wrong tree; refuse before fetching, in --dry-run too.
	if err := jobexclusive.EnsureEnvironmentNotExclusivelyHeld(ctx, "gate-merge", params.UnderLeaseID); err != nil {
		return model.GateMergeWorkingTreeResult{}, err
	}

	clean, err := deps.WorkingTreeClean(ctx, root)
	if err != nil {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("check working tree is clean: %w", err)
	}
	if !clean {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("refusing to gate-merge: the working tree has uncommitted changes")
	}

	targetRef := gateMergeFetchRef(target)
	fetchArgs := traceGateMergePlan(ctx, root, params.Sources, target, remote, targetRef)
	if ctx.DryRun {
		return model.GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndGateMergeWorkingTree(ctx, root, params.Sources, target, remote, fetchArgs, targetRef, deps)
}

func validateGateMergeSources(sources []model.GateMergeSource) error {
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
func traceGateMergePlan(ctx core.Context, root string, sources []model.GateMergeSource, target, remote, targetRef string) []string {
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

func fetchAndGateMergeWorkingTree(ctx core.Context, root string, sources []model.GateMergeSource, target, remote string, fetchArgs []string, targetRef string, deps GateMergeWorkingTreeDependencies) (model.GateMergeWorkingTreeResult, error) {
	if output, err := runGitCapturingOutput(root, deps, fetchArgs...); err != nil {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("git fetch: %w: %s", err, output)
	}

	if output, err := runGitCapturingOutput(root, deps, "checkout", "-B", target, targetRef); err != nil {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("git checkout: %w: %s", err, output)
	}

	result := model.GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}
	tip, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return model.GateMergeWorkingTreeResult{}, fmt.Errorf("resolve target tip: %w", err)
	}
	result.Commit = tip

	for _, source := range sources {
		landed, skipped, err := gateMergeOneSource(ctx, root, source, remote, targetRef, deps)
		if err != nil {
			return model.GateMergeWorkingTreeResult{}, err
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
func gateMergeOneSource(ctx core.Context, root string, source model.GateMergeSource, remote, targetRef string, deps GateMergeWorkingTreeDependencies) (*model.GateMergeLandedSource, *model.GateMergeSkippedSource, error) {
	sourceRef := gateMergeFetchRef(source.Branch)
	sourceCommit, err := deps.ResolveRef(ctx, root, sourceRef)
	if err != nil {
		return nil, &model.GateMergeSkippedSource{
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
		return nil, &model.GateMergeSkippedSource{
			SourceBranch: source.Branch,
			SourceCommit: sourceCommit,
			Reason:       fmt.Sprintf("squashing %s onto %s staged no changes: the source is already contained in the target", source.Branch, remote),
		}, nil
	}

	return commitGateMergeSource(ctx, root, source, sourceCommit, sourceRef, targetRef, staged, deps)
}

func commitGateMergeSource(ctx core.Context, root string, source model.GateMergeSource, sourceCommit, sourceRef, targetRef string, staged []string, deps GateMergeWorkingTreeDependencies) (*model.GateMergeLandedSource, *model.GateMergeSkippedSource, error) {
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

	return &model.GateMergeLandedSource{
		SourceBranch:    source.Branch,
		SourceCommit:    sourceCommit,
		Commit:          commit,
		CarriedTrailers: trailers,
	}, nil, nil
}

// refuseGateMergeSourceOnPolicy backs out and skips a source that fails the regression policy.
func refuseGateMergeSourceOnPolicy(root string, source model.GateMergeSource, sourceCommit string, bodies, staged []string, deps GateMergeWorkingTreeDependencies) (model.GateMergeSkippedSource, bool, error) {
	readFile := func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) }
	policyErr := evaluateMergeRegressionPolicy(source.Branch, bodies, staged, readFile)
	if policyErr == nil {
		return model.GateMergeSkippedSource{}, false, nil
	}
	var resetStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
		return model.GateMergeSkippedSource{}, false, fmt.Errorf("git reset --hard after refusing %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
	}
	return model.GateMergeSkippedSource{
		SourceBranch: source.Branch,
		SourceCommit: sourceCommit,
		Reason:       "regression-test policy: " + policyErr.Error(),
	}, true, nil
}

// skipConflictedGateMergeSource returns skip=false with nil error when every conflict was a
// regenerated atlas.sum; a non-nil error is fatal for the batch.
func skipConflictedGateMergeSource(root string, source model.GateMergeSource, remote, sourceCommit, sourceRef, mergeOutput string, mergeErr error, deps GateMergeWorkingTreeDependencies) (model.GateMergeSkippedSource, bool, error) {
	conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
	if conflictErr != nil || len(conflicted) == 0 {
		return model.GateMergeSkippedSource{}, false, fmt.Errorf("git merge --squash %s: %w: %s", sourceRef, mergeErr, mergeOutput)
	}
	remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
	if resolveErr != nil {
		return model.GateMergeSkippedSource{}, false, fmt.Errorf("regenerate conflicted atlas.sum for %s: %w", source.Branch, resolveErr)
	}
	if len(remaining) == 0 {
		return model.GateMergeSkippedSource{}, false, nil
	}
	var resetStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
		return model.GateMergeSkippedSource{}, false, fmt.Errorf("git reset --hard after a conflicted squash of %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
	}
	return model.GateMergeSkippedSource{
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
