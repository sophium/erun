package shared

import (
	"os"
	"path/filepath"
	"strings"
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
