package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// Every agent task that clones the repo into a work directory leaves that
// clone behind once its job finishes -- nothing ever reclaimed it, so the
// work directory grows without bound. The obvious fix, sweeping the whole
// directory on a timer or `rm -rf`ing anything old, is destructive: a clone
// can hold commits that exist nowhere else (never pushed) or a dirty tree
// (never committed at all), and losing either is real work gone, not disk
// reclaimed. This file is the safety check that has to hold before any clone
// is removed, plus the one place it is actually acted on: the same job-finish
// hook that already checkpoints a dirty tree (job_worktree.go).
//
// "Finished" is answered by construction rather than by a timestamp or a
// directory scan: reclaim only ever runs once, from inside finishEnvironmentJob,
// at the exact moment the supervisor that owns this job observes its process
// exit. There is no window in which a running job's directory could be
// mistaken for an idle one, because nothing here ever looks at an arbitrary
// directory's mtime to guess whether work on it has stopped.

// WorkCloneUserHomeDir is the seam a test overrides to point workCloneRoot at
// a fixture home directory instead of the real one.
var WorkCloneUserHomeDir = os.UserHomeDir

// WorkCloneRoot resolves the directory that holds per-task agent clones
// (conventionally /home/erun/work), distinct from the tenant's own runtime
// repository (/home/erun/git/<tenant>). Reclaim only ever acts on a path
// under this root -- see isUnderWorkRoot -- which is what keeps the runtime
// repo itself unreachable no matter what a caller happens to set a job's Dir
// to.
func WorkCloneRoot(homeDir string) (string, error) {
	resolved := strings.TrimSpace(homeDir)
	if resolved == "" {
		home, err := WorkCloneUserHomeDir()
		if err != nil {
			return "", err
		}
		resolved = home
	}
	return filepath.Join(resolved, "work"), nil
}

// ResolveBranchUpstreamCandidate answers "what would branch be compared
// against to know it is safe" -- branch's configured @{u} when it has one,
// falling back to a same-named branch on origin otherwise. The fallback
// matters because a plain `git push origin branch:branch` (exactly what the
// job-finish checkpoint in job_worktree.go does) lands the commit on the
// remote without ever configuring `@{u}`, so a missing upstream is not proof
// nothing was pushed -- it is only proof that *if* something was pushed, it
// was not pushed the way `-u` would have recorded it.
func ResolveBranchUpstreamCandidate(ctx core.Context, dir, branch string) (string, bool) {
	if upstream, ok := gitUpstreamRef(ctx, dir, branch); ok {
		return upstream, true
	}
	candidate := "origin/" + branch
	if _, ok, err := GitResolvedRef(ctx, dir, candidate); err == nil && ok {
		return candidate, true
	}
	return "", false
}

func gitUpstreamRef(ctx core.Context, dir, branch string) (string, bool) {
	ref := branch + "@{u}"
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--abbrev-ref", ref)
	output, err := core.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", ref).Output()
	if err != nil {
		return "", false
	}
	upstream := strings.TrimSpace(string(output))
	if upstream == "" {
		return "", false
	}
	return upstream, true
}

func GitCommitsAhead(ctx core.Context, dir, base, tip string) (int, error) {
	rangeSpec := base + ".." + tip
	ctx.TraceCommand("", "git", "-C", dir, "rev-list", "--count", rangeSpec)
	output, err := core.Command("git", "-C", dir, "rev-list", "--count", rangeSpec).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return 0, fmt.Errorf("%w: %s", err, stderr)
		}
		return 0, err
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(string(output)))
	if convErr != nil {
		return 0, convErr
	}
	return count, nil
}
