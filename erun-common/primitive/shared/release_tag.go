package shared

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

func shouldSkipExistingReleaseTag(args []string) bool {
	return len(args) >= 3 && args[0] == "tag" && args[1] == "-a" && strings.TrimSpace(args[2]) != ""
}

func prepareReleaseTag(ctx core.Context, spec ReleaseSpec, runGit gitx.GitCommandRunnerFunc, command ReleaseCommandSpec) (bool, error) {
	tag := strings.TrimSpace(command.Args[2])
	if tag == "" {
		return false, nil
	}
	if spec.Force {
		if err := deleteExistingReleaseTag(ctx, command.Dir, tag, runGit); err != nil {
			return false, err
		}
		return false, nil
	}
	return CanSkipExistingReleaseTag(ctx, spec, command.Dir, tag, runGit)
}

func CanSkipExistingReleaseTag(ctx core.Context, spec ReleaseSpec, projectRoot, tag string, runGit gitx.GitCommandRunnerFunc) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}

	tagCommit, ok, err := GitResolvedRef(ctx, projectRoot, tag+"^{}")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	headCommit, ok, err := GitResolvedRef(ctx, projectRoot, "HEAD")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("could not resolve HEAD for release tag check")
	}
	if tagCommit != headCommit {
		return false, releaseTagMismatchError(ctx, spec, projectRoot, tag, tagCommit, headCommit, runGit)
	}

	return true, nil
}

// releaseTagMismatchError diagnoses a tag that exists but not at HEAD. When it
// is unpushed and the release branch's remote history has never incorporated
// it, that is not a real collision — it is a leftover local tag+commit from a
// release that was interrupted before it published anything (e.g. the
// pod holding the worktree was replaced mid-release). Reclaiming it
// automatically is judged too aggressive for a git tag inside a release flow,
// so the refusal instead names the diagnosis and the exact remedy, rather than
// leaving the operator to work out both by hand.
func releaseTagMismatchError(ctx core.Context, spec ReleaseSpec, projectRoot, tag, tagCommit, headCommit string, runGit gitx.GitCommandRunnerFunc) error {
	base := fmt.Errorf("release tag %q already exists at %s, expected current HEAD %s", tag, tagCommit, headCommit)
	pushed, err := GitRemoteTagExists(ctx, projectRoot, "origin", tag)
	if err != nil || pushed {
		return base
	}
	reachable, known := releaseBaseBranchIncorporates(projectRoot, spec.Branch, tagCommit, runGit)
	if !known || reachable {
		return base
	}
	return fmt.Errorf("%w\na previous run left an unpushed local tag %q at %s that origin/%s has never incorporated — most likely a release interrupted before it published anything; delete it with `git tag -d %s` to retry",
		base, tag, tagCommit, spec.Branch, tag)
}

// releaseBaseBranchIncorporates reports whether commit is already part of the
// release branch's remote history, fetching it fresh the same way
// releaseBaseBranchAhead does. known is false when the read was inconclusive
// (no remote, no network, unresolved ref) — an unknown answer is never
// treated as "reachable" or "unreachable", since either would risk offering
// the reclaim message, or withholding it, on a guess.
func releaseBaseBranchIncorporates(projectRoot, branch, commit string, runGit gitx.GitCommandRunnerFunc) (reachable bool, known bool) {
	branch = strings.TrimSpace(branch)
	if branch == "" || commit == "" {
		return false, false
	}
	if err := runGit(projectRoot, io.Discard, io.Discard, "fetch", "origin", branch); err != nil {
		return false, false
	}
	err := core.Command("git", "-C", projectRoot, "merge-base", "--is-ancestor", commit, "FETCH_HEAD").Run()
	if err == nil {
		return true, true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, true
	}
	return false, false
}

// ReleaseStampCommitPrefix is the subject newReleaseStage's stamp commit
// always carries ("[skip ci] release <version>"), so a retry can recognize
// its own leftover stamp commits sitting at HEAD.
const ReleaseStampCommitPrefix = "[skip ci] release "

// releaseStampWalkLimit bounds how many leading stamp commits are ever
// walked past when looking for a leftover. A release stamps at most once per
// attempt, so a real chain this long could only be repeated failed retries,
// never legitimate history; the cap keeps a pathological repository from
// turning this into an unbounded run of fetches.
const releaseStampWalkLimit = 20

// ResolveReleaseVersionCommit is the commit whose short sha names a
// candidate/prerelease version (resolveReleaseVersion). Root AGENTS.md
// "Release Rules" calls the pre-publish stages "recoverable by re-running",
// but a release that fails after its "release" stage stamp leaves that stamp
// sitting, unpushed, at HEAD — naming the retry's version after the stamp
// rather than the change actually being released stacks another stamp on
// the next attempt (each one resolving yet another wrong version) and
// invalidates the build fingerprint cache, since the stamp changes tracked
// files. Skipping past a leading run of such stamps is safe only
// while each one is unpushed and origin's branch history has never
// incorporated it — the same leftover-vs-real-collision boundary
// releaseTagMismatchError already draws for the release tag.
func ResolveReleaseVersionCommit(ctx core.Context, projectRoot, branch string, resolveCommit gitx.GitValueResolverFunc, runGit gitx.GitCommandRunnerFunc) (string, error) {
	base, found, err := FindLeftoverReleaseStampBase(ctx, projectRoot, branch, runGit)
	if err != nil {
		return "", err
	}
	if !found {
		return resolveCommit(ctx, projectRoot)
	}
	ctx.Trace("release: HEAD is a leftover, unpushed release stamp from an earlier attempt; resolving the version from " + base + " instead")
	return gitShortCommitOf(ctx, projectRoot, base)
}

// FindLeftoverReleaseStampBase walks HEAD's parents past a leading run of
// the release stage's own stamp commits, stopping at the first commit that
// either is not a stamp or is already reachable from origin/branch — a real,
// previously published stamp, not an interrupted attempt's leftover. found
// is false when HEAD carries no such leftover, meaning version resolution
// should keep using HEAD as before.
func FindLeftoverReleaseStampBase(ctx core.Context, projectRoot, branch string, runGit gitx.GitCommandRunnerFunc) (string, bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", false, nil
	}

	base, ok, err := GitResolvedRef(ctx, projectRoot, "HEAD")
	if err != nil || !ok {
		return "", false, err
	}

	found := false
	for i := 0; i < releaseStampWalkLimit; i++ {
		leftover, err := isLeftoverReleaseStampCommit(projectRoot, branch, base, runGit)
		if err != nil {
			return "", false, err
		}
		if !leftover {
			break
		}
		parent, ok, err := GitResolvedRef(ctx, projectRoot, base+"^")
		if err != nil {
			return "", false, err
		}
		if !ok {
			break
		}
		base = parent
		found = true
	}
	if !found {
		return "", false, nil
	}
	return base, true, nil
}

// isLeftoverReleaseStampCommit reports whether commit is one of the release
// stage's own stamp commits (by subject) that is also unpushed and not yet
// incorporated by origin/branch — the two conditions that together make it a
// safe-to-skip-past leftover from an interrupted attempt rather than real,
// published history.
func isLeftoverReleaseStampCommit(projectRoot, branch, commit string, runGit gitx.GitCommandRunnerFunc) (bool, error) {
	subject, err := GitCommitSubject(projectRoot, commit)
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(subject, ReleaseStampCommitPrefix) {
		return false, nil
	}
	reachable, known := releaseBaseBranchIncorporates(projectRoot, branch, commit, runGit)
	return known && !reachable, nil
}

func deleteExistingReleaseTag(ctx core.Context, projectRoot, tag string, runGit gitx.GitCommandRunnerFunc) error {
	localExists, err := gitTagExists(ctx, projectRoot, tag)
	if err != nil {
		return err
	}
	if localExists {
		ctx.TraceCommand(projectRoot, "git", "tag", "-d", tag)
		if !ctx.DryRun {
			if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "tag", "-d", tag); err != nil {
				return err
			}
		}
	}

	remoteExists, err := GitRemoteTagExists(ctx, projectRoot, "origin", tag)
	if err != nil {
		return err
	}
	if remoteExists {
		ctx.TraceCommand(projectRoot, "git", "push", "--delete", "origin", tag)
		if !ctx.DryRun {
			var pushOutput strings.Builder
			if err := runReleasePush(ctx, projectRoot, runGit, &pushOutput, "push", "--delete", "origin", tag); err != nil {
				return err
			}
		}
	}

	return nil
}
