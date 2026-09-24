package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// GateMergeSource is one branch to squash into the prospective merge, in the
// order it should land. Message becomes that branch's own squash commit
// message — normally the branch's review name (AGENTS.md: "Review name is
// the squash merge message") — since a landed branch becomes a real commit
// on TargetBranch if the gate passes. The branch's own load-bearing trailers
// (see gateMergeCarriedTokens) are appended beneath it, so what the branch
// declared about its own commit survives a squash that would otherwise
// replace the branch's history with this one message.
type GateMergeSource struct {
	Branch  string
	Message string
}

// GateMergeWorkingTreeParams squash-merges Sources onto a fresh local
// checkout of TargetBranch, one commit per landed source — the git half of
// gating a merge queue promotion. The environment a review's merge queue
// promotes to MERGE runs this before `erun build`: fetch every branch, build
// the prospective merge onto the target's *current* remote tip (never the
// working tree's own existing branch, and never a source branch as its
// author left it), leaving a stack of squash commits ready to gate before
// anything is pushed.
//
// A single review's ordinary gate is the Sources-of-one case. Batching
// (Sources with more than one entry) is what lets a caller test whether
// several unmerged branches compile *together*, not just individually —
// something no repeated single-source call can see, since each call resets
// the working tree from the target's remote tip and discards whatever an
// earlier call landed.
type GateMergeWorkingTreeParams struct {
	// Sources are the branches to squash in, in order. At least one is
	// required.
	Sources []GateMergeSource
	// TargetBranch is the branch the squash merges land onto, checked out
	// fresh from its own current remote tip — not from the working tree's
	// checked-out branch, which this leaves behind.
	TargetBranch string
	// Remote is the git remote every branch is fetched from. Empty defaults
	// to "origin".
	Remote string
	// UnderLeaseID names an exclusive environment claim the caller already
	// holds, so a drive that took the environment for its own whole window is
	// not refused by its own claim. Empty for a caller holding nothing, which
	// is then refused by any claim it finds.
	UnderLeaseID string
}

// GateMergeLandedSource is one source branch that squash-merged cleanly.
type GateMergeLandedSource struct {
	SourceBranch string `json:"sourceBranch"`
	SourceCommit string `json:"sourceCommit"`
	// Commit is the squash commit that landed this branch.
	Commit string `json:"commit"`
	// CarriedTrailers names the branch's own load-bearing trailers this squash
	// appended beneath the caller's message, in the order they were read. It is
	// reported rather than left implicit because an empty list is otherwise
	// indistinguishable from a carriage that found nothing to carry: the squash
	// message looks the same, the gate-merge reports success, and the missing
	// "Closes #N" is only discovered later by reading the landed commit. Always
	// serialized, including when empty, so a caller never has to tell "carried
	// none" from "this field was not read".
	CarriedTrailers []string `json:"carriedTrailers"`
}

// GateMergeSkippedSource is one source branch that did not land — a
// conflicting squash, a branch this could not even resolve after fetching
// (e.g. deleted since the caller decided to batch it), or one whose squash
// staged nothing because its content is already on the target. The rest of
// the batch still gates; a skip here is reported, not fatal.
type GateMergeSkippedSource struct {
	SourceBranch    string   `json:"sourceBranch"`
	SourceCommit    string   `json:"sourceCommit,omitempty"`
	Reason          string   `json:"reason"`
	ConflictedFiles []string `json:"conflictedFiles,omitempty"`
}

// GateMergeWorkingTreeResult is what actually landed. Commit is the tip of
// the resulting stack — the last landed source's squash commit, or the
// target's own unchanged tip when every source was skipped.
type GateMergeWorkingTreeResult struct {
	TargetBranch string                   `json:"targetBranch"`
	Remote       string                   `json:"remote"`
	Commit       string                   `json:"commit"`
	Landed       []GateMergeLandedSource  `json:"landed"`
	Skipped      []GateMergeSkippedSource `json:"skipped,omitempty"`
}

// GateMergeWorkingTreeDependencies lets tests replace the git plumbing
// without a real remote, mirroring MergeWorkingTreeBranchDependencies.
type GateMergeWorkingTreeDependencies struct {
	WorkingTreeClean func(ctx Context, root string) (bool, error)
	ResolveRef       func(ctx Context, root, ref string) (string, error)
	RunGit           GitCommandRunnerFunc
	ConflictedFiles  func(root string, runGit GitCommandRunnerFunc) ([]string, error)
	// StagedPaths reads back what a squash actually staged. A source that
	// stages nothing contributes nothing to the prospective merge, so it is
	// skipped rather than committed — see gateMergeOneSource.
	StagedPaths func(ctx Context, root string) ([]string, error)
	// RunAtlasHash regenerates a conflicted atlas.sum instead of leaving it
	// for a human to resolve. See resolveAtlasSumConflicts.
	RunAtlasHash AtlasMigrateHashRunnerFunc
}

func normalizeGateMergeWorkingTreeDependencies(deps GateMergeWorkingTreeDependencies) GateMergeWorkingTreeDependencies {
	if deps.WorkingTreeClean == nil {
		deps.WorkingTreeClean = gitWorktreeClean
	}
	if deps.ResolveRef == nil {
		deps.ResolveRef = gitResolveRef
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	if deps.ConflictedFiles == nil {
		deps.ConflictedFiles = gitMergeConflictedFiles
	}
	if deps.StagedPaths == nil {
		deps.StagedPaths = gitStagedFiles
	}
	if deps.RunAtlasHash == nil {
		deps.RunAtlasHash = runAtlasMigrateHash
	}
	return deps
}

// GateMergeWorkingTree fetches TargetBranch and every source in Params.Sources
// from Remote into the local refs/erun/gate-merge/ staging namespace, checks
// out a local branch named TargetBranch at its own fresh remote tip, then
// squash-merges each source onto it in order, each as
// its own commit. A source whose squash conflicts is skipped — the merge is
// aborted, the conflict recorded in the result's Skipped list, and the next
// source is tried against the working tree as it stood before that attempt
// — rather than failing the whole batch, so one bad branch cannot turn an
// otherwise-clean batch dead. A source whose squash stages nothing is
// skipped the same way: it contributes no change, which is a no-op rather
// than an error, and it is recorded so the caller can tell "already landed"
// from "broken". Sources is required to be non-empty.
//
// The working tree must be clean before this runs: unlike the ordinary
// exec merge/commit/push primitives, this checks out a different local
// branch than whatever the tree is currently on, so an in-progress change
// left uncommitted there would otherwise be silently carried onto the
// prospective merge or lost.
func GateMergeWorkingTree(ctx Context, root string, params GateMergeWorkingTreeParams, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
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

	// A gate-merge rewrites the environment's one shared worktree onto the
	// target branch, so two of them in flight at once are not merely slow —
	// they are wrong: one merge-queue drive reported pushing a commit that
	// belonged to the other batch's tree, and two pull requests were closed
	// against work that had not landed. Refused here, before the fetch, so a
	// drive that has lost the environment stops without touching the tree at
	// all. Checked in --dry-run too, for the same reason the clean check below
	// is: it is a read, and a dry run should refuse what a real run refuses.
	if err := EnsureEnvironmentNotExclusivelyHeld(ctx, "gate-merge", params.UnderLeaseID); err != nil {
		return GateMergeWorkingTreeResult{}, err
	}

	// Checked even during --dry-run, the same discipline CommitWorkingTree and
	// PushWorkingTreeBranch apply to their own branch-mismatch check: it is a
	// read, not a mutation, and a dry run should refuse exactly what a real run
	// would refuse.
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

// validateGateMergeSources checks that every source names a branch and a
// commit message, isolated so GateMergeWorkingTree's own branching stays low
// enough for the cyclomatic-complexity gate.
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

// gateMergeCarriedTrailerLines is the closed set of trailers a squash carries
// up into the commit that lands on the target, one pattern per line so the
// shape each token's value must have sits beside the token. These are the
// lines this repository's own guidance makes load-bearing for a landed commit
// — "Closes #N" from the contribution rules, and the regression-reproduction
// trailers from "A Defect Fix Names Its Reproduction" — because they are
// declarations about the change that a later reader looks for on the target,
// not commentary the branch author happened to leave behind. Copying every
// trailing line a branch's commits might contain onto an unattended commit on
// the target is a larger surface than this defect needs; a token not listed
// here stays with the branch's own history.
//
// The issue-closing line is spelled without a colon, unlike the others, and
// its value is required to be an issue reference: a sentence that merely
// begins with the word cannot be read as a trailer.
var gateMergeCarriedTrailerLines = []*regexp.Regexp{
	regexp.MustCompile(`^Closes[ \t]+#[0-9]+(?:[ \t]*,[ \t]*#[0-9]+)*$`),
	regexp.MustCompile(`^Reproduces:[ \t]+\S.*$`),
	regexp.MustCompile(`^Regression-Test:[ \t]+\S.*$`),
	regexp.MustCompile(`^Regression-Test-Existing:[ \t]+\S.*$`),
	regexp.MustCompile(`^Regression-Test-Exemption:[ \t]+\S.*$`),
}

// gateMergeCarriesTrailer reports whether one line is a trailer this squash
// preserves.
func gateMergeCarriesTrailer(line string) bool {
	for _, pattern := range gateMergeCarriedTrailerLines {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// gateMergeStartsTrailerBlock reports whether one line begins a new trailer
// entry. Any token-shaped line does, carried or not, so an unrecognised
// trailer between two carried ones does not fold the second into the first as
// an indented continuation.
var gateMergeStartsTrailerBlock = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*(?::|[ \t])`)

// gateMergeTrailerLogArgs is the git invocation that reads back a source
// branch's own commit bodies. The base is the ref the source is being squashed
// onto, so the range is the commits this branch contributes to the prospective
// merge rather than everything it has ever contained — a source that was cut
// from an already-landed branch does not re-declare that branch's trailers.
// Bodies are NUL-separated so a body containing blank lines still parses.
func gateMergeTrailerLogArgs(base, sourceRef string) []string {
	return []string{"log", "--format=%B%x00", base + ".." + sourceRef}
}

// gateMergeCarriedTrailers reads the trailers a source branch's own commits
// declare, in the order they appear. Empty means the branch declares none,
// which is not an error. A git failure here is fatal for the batch rather than
// silently absent: the whole point of reading these is that their absence is
// invisible on the target, so a read that failed must not be swallowed into
// the same empty result as a branch that declared nothing.
func gateMergeCarriedTrailers(ctx Context, root, base, sourceRef string, deps GateMergeWorkingTreeDependencies) ([]string, error) {
	args := gateMergeTrailerLogArgs(base, sourceRef)
	ctx.TraceCommand(root, "git", args...)
	output, err := runGitCapturingOutput(root, deps, args...)
	if err != nil {
		return nil, fmt.Errorf("read the trailers on %s: %w", sourceRef, err)
	}
	var trailers []string
	for _, body := range strings.Split(output, "\x00") {
		trailers = append(trailers, gateMergeTrailersFromBody(body)...)
	}
	return trailers, nil
}

// gateMergeTrailersFromBody extracts the carried trailer entries from one
// commit body. Every line of the body is considered, and an entry extends over
// the indented continuation lines git folds into it, so a wrapped "Reproduces:"
// arrives whole rather than truncated at its first line break.
//
// There is deliberately no "trailer block" boundary deciding which lines are
// looked at. Every such boundary this has had was a way to lose a declaration
// silently: reading only the body's final run of non-empty lines discarded
// everything above a trailing "Co-Authored-By:", and walking back a paragraph
// at a time until a paragraph stopped looking like a trailer discarded
// everything above a closing paragraph the walk did not recognise — a bare
// "Refs #N" is the shape that did it, since it carries no colon to read as a
// token. The author's declarations are not the part of a commit body whose
// position makes them provisional, and the gate-merge reports success either
// way, so the loss is invisible on the target. What decides carriage is the
// closed set in gateMergeCarriedTrailerLines, which is narrow enough that a
// line it matches is a declaration wherever it sits and a body of ordinary
// prose matches none of it.
func gateMergeTrailersFromBody(body string) []string {
	return gateMergeTrailerEntries(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
}

// gateMergeTrailerEntries picks the carried entries out of a commit body's
// lines, folding each one's indented continuation lines into it so a wrapped
// "Reproduces:" arrives whole rather than truncated at its first line break.
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
		if gateMergeStartsTrailerBlock.MatchString(line) {
			flush()
			if gateMergeCarriesTrailer(line) {
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

// gateMergeContinuesTrailer reports whether a line is an indented continuation
// of the trailer above it, which is the shape git folds into one entry.
func gateMergeContinuesTrailer(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// gateMergeCommitMessage is the message one source lands under: the caller's
// message — the review name — with the branch's own declared trailers beneath
// it. The caller's message stays the subject line and the whole of the body it
// already carries; a trailer the caller already passed through, or one two
// commits on the branch both declare, is written once.
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

// gateMergeFetchRef is the local ref a gate-merge stages each fetched branch
// under. A gate-merge cannot consume the remote-tracking name it used to
// (origin/main): ref names may contain neither ":" nor "//", so no such name
// exists when --remote is a URL rather than a configured remote, and both the
// checkout and every squash-merge failed against a ref git could not resolve.
// Staging by branch name gives one shape that works for either kind of remote,
// and --dry-run now traces exactly the refs the real run resolves.
func gateMergeFetchRef(branch string) string {
	return "refs/erun/gate-merge/" + branch
}

// gateMergeFetchSpec maps a remote branch onto its staging ref. The leading
// "+" forces the update so a reused environment never gates against a tip left
// behind by an earlier drive. The source is left unqualified so git keeps
// resolving it as it did before (refs/heads, then refs/tags), rather than
// narrowing a gate-merge to branches only.
func gateMergeFetchSpec(branch string) string {
	return "+" + branch + ":" + gateMergeFetchRef(branch)
}

// traceGateMergePlan emits the trace lines for the fetch, the checkout, and
// each source's squash-merge + commit pair, and returns the fetch argv so
// the real run doesn't have to rebuild it. Traced unconditionally (not only
// under --dry-run), matching every other exec primitive's audit contract.
func traceGateMergePlan(ctx Context, root string, sources []GateMergeSource, target, remote, targetRef string) []string {
	fetchArgs := []string{"fetch", remote, gateMergeFetchSpec(target)}
	for _, source := range sources {
		fetchArgs = append(fetchArgs, gateMergeFetchSpec(source.Branch))
	}
	ctx.TraceCommand(root, "git", fetchArgs...)
	ctx.TraceCommand(root, "git", "checkout", "-B", target, targetRef)
	for _, source := range sources {
		ctx.TraceCommand(root, "git", "merge", "--squash", gateMergeFetchRef(source.Branch))
		ctx.TraceCommand(root, "git", gateMergeTrailerLogArgs(targetRef, gateMergeFetchRef(source.Branch))...)
		ctx.TraceCommand(root, "git", "commit", "-m", "<message + the branch trailers>")
	}
	return fetchArgs
}

// fetchAndGateMergeWorkingTree runs the mutating half of GateMergeWorkingTree,
// isolated so the validation and dry-run branching above it don't inflate
// that function's complexity.
func fetchAndGateMergeWorkingTree(ctx Context, root string, sources []GateMergeSource, target, remote string, fetchArgs []string, targetRef string, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
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

// runGitCapturingOutput runs one git command with both of its streams
// collected into a single buffer, returning that buffer trimmed. A caller
// wrapping an error to explain a failure must not read only one stream: git
// reports some failures on stdout, and "nothing to commit, working tree
// clean" — the one that says a squash staged nothing — is exactly one, so a
// stderr-only buffer reduces a named cause to a bare exit status. One
// *bytes.Buffer is safe for both streams because os/exec detects identical
// writers and drains them through a single pipe.
func runGitCapturingOutput(root string, deps GateMergeWorkingTreeDependencies, args ...string) (string, error) {
	var output bytes.Buffer
	err := deps.RunGit(root, &output, &output, args...)
	return strings.TrimSpace(output.String()), err
}

// gateMergeOneSource squash-merges and commits one source branch onto
// whatever the working tree currently holds. A conflicted squash is backed
// out with `git reset --hard HEAD` and reported as a skip rather than
// returned as an error, so the caller can keep trying the rest of the batch
// against a clean tree — `git merge --abort` is not available here, since
// `--squash` deliberately never records a MERGE_HEAD to abort. A squash that
// stages nothing is likewise a skip: the source contributes no change, so
// there is nothing to commit and no reason to fail the batch. Any other git
// failure (a bad ref, a real I/O error) is fatal for the whole batch, since
// it says something is wrong beyond this one branch.
func gateMergeOneSource(ctx Context, root string, source GateMergeSource, remote, targetRef string, deps GateMergeWorkingTreeDependencies) (*GateMergeLandedSource, *GateMergeSkippedSource, error) {
	sourceRef := gateMergeFetchRef(source.Branch)
	sourceCommit, err := deps.ResolveRef(ctx, root, sourceRef)
	if err != nil {
		return nil, &GateMergeSkippedSource{
			SourceBranch: source.Branch,
			Reason:       fmt.Sprintf("could not resolve %s after fetch: %v", sourceRef, err),
		}, nil
	}

	if mergeOutput, err := runGitCapturingOutput(root, deps, "merge", "--squash", sourceRef); err != nil {
		skipped, skipErr := skipConflictedGateMergeSource(root, source, remote, sourceCommit, sourceRef, mergeOutput, err, deps)
		if skipped != nil || skipErr != nil {
			return nil, skipped, skipErr
		}
		// Every conflict was an atlas.sum this regenerated and staged — fall
		// through and land this source like a clean squash.
	}

	// A squash that staged nothing contributes nothing to the prospective
	// merge: the source's content is already on the target, whether because
	// it merged to no tree change at all or because it is already contained
	// there. There is no commit to record, and asking git for one anyway
	// aborted the whole batch — the one channel that explained why, git's own
	// "nothing to commit, working tree clean", is on *stdout*, which the
	// commit call discarded, so the caller saw a bare exit status. A no-op
	// source is the same class of thing as a conflicting one: skipped and
	// recorded, with the rest of the batch still gating.
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

	trailers, err := gateMergeCarriedTrailers(ctx, root, targetRef, sourceRef, deps)
	if err != nil {
		return nil, nil, err
	}
	if output, err := runGitCapturingOutput(root, deps, "commit", "-m", gateMergeCommitMessage(source.Message, trailers)); err != nil {
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

// skipConflictedGateMergeSource classifies a failed `git merge --squash` for
// one source. A nil skip with a nil error means the source is not skipped
// after all: every conflict was an atlas.sum that resolveAtlasSumConflicts
// regenerated and staged, so the caller lands it like a clean squash. A non-nil
// skip is a recorded non-fatal skip; a non-nil error is fatal for the whole
// batch. Isolated so gateMergeOneSource's own branching stays under the
// cyclomatic-complexity gate.
func skipConflictedGateMergeSource(root string, source GateMergeSource, remote, sourceCommit, sourceRef, mergeOutput string, mergeErr error, deps GateMergeWorkingTreeDependencies) (*GateMergeSkippedSource, error) {
	conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
	if conflictErr != nil || len(conflicted) == 0 {
		return nil, fmt.Errorf("git merge --squash %s: %w: %s", sourceRef, mergeErr, mergeOutput)
	}
	remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
	if resolveErr != nil {
		return nil, fmt.Errorf("regenerate conflicted atlas.sum for %s: %w", source.Branch, resolveErr)
	}
	if len(remaining) == 0 {
		return nil, nil
	}
	var resetStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
		return nil, fmt.Errorf("git reset --hard after a conflicted squash of %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
	}
	return &GateMergeSkippedSource{
		SourceBranch:    source.Branch,
		SourceCommit:    sourceCommit,
		Reason:          fmt.Sprintf("squashing %s onto %s left %d file(s) conflicted", source.Branch, remote, len(remaining)),
		ConflictedFiles: remaining,
	}, nil
}

// gitResolveRef resolves ref to its full commit hash.
func gitResolveRef(ctx Context, root, ref string) (string, error) {
	ctx.TraceCommand("", "git", "-C", root, "rev-parse", ref)
	output, err := Command("git", "-C", root, "rev-parse", ref).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
