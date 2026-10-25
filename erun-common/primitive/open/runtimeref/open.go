package runtimeref

import (
	"path"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func RemoteShellWorktreePath(req configpkg.ShellLaunchParams) string {
	return RemoteWorktreePath(req)
}

func RemoteWorktreePathForRepoName(repoName string) string {
	repoName = strings.TrimSpace(repoName)
	if repoName == "" {
		repoName = "worktree"
	}
	return path.Join("/home", "erun", "git", repoName)
}

// APIDeploymentName is templated from `.Values.tenant`, not the Helm release name.
func APIDeploymentName(tenant string) string {
	return opentarget.TenantResourcePrefix(tenant) + "-api"
}

func RemoteWorktreePath(req configpkg.ShellLaunchParams) string {
	return RemoteWorktreePathForRepoName(remoteWorktreeRepoName(req))
}

func remoteWorktreeRepoName(req configpkg.ShellLaunchParams) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(req.Dir)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return "worktree"
	}
	return repoName
}
