package repopath

import (
	"os"

	"github.com/sophium/erun/erun-common/base/config"
)

// TenantLocalCheckoutRoot finds the tenant's repo on this machine via a sibling env with a
// local, existing worktree.
func TenantLocalCheckoutRoot(envs []config.EnvConfig) (string, bool) {
	for _, candidate := range envs {
		if candidate.RemoteWorktree() {
			continue
		}
		path := candidate.EffectiveLocalRepoPath()
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		return path, true
	}
	return "", false
}
