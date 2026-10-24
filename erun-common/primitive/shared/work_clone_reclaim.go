package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// Agent work clones are reclaimed only from finishEnvironmentJob, once the job's process has
// exited, and only when nothing unpushed or uncommitted would be lost.

// WorkCloneUserHomeDir is a test seam for WorkCloneRoot's home directory.
var WorkCloneUserHomeDir = os.UserHomeDir

// WorkCloneRoot is the per-task clone root (/home/erun/work); reclaim acts only under it,
// keeping the runtime repository unreachable.
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

// ResolveBranchUpstreamCandidate returns branch's @{u}, else origin/<branch>: the job-finish
// checkpoint pushes without -u, so a missing upstream is not proof nothing was pushed.
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
