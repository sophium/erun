package eruncommon

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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
	if _, ok, err := shared.GitResolvedRef(ctx, dir, candidate); err == nil && ok {
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
