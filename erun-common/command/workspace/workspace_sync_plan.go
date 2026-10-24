package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Distinct refusals: the operator's fix differs, though the empty mirror looks the same.
var (
	ErrWorkspaceSyncNotRemoteAgent = errors.New("only a remote-agent environment has a pod worktree to mirror")
	ErrWorkspaceSyncNotEnabled     = errors.New("workspace sync is not enabled for this environment")
	ErrWorkspaceSyncNoLocalPath    = errors.New("workspace sync has no local path to mirror into")
)

// WorkspaceSyncLocalPath prefers the configured path, then the project root, then a host-local worktree.
func WorkspaceSyncLocalPath(result config.OpenResult, findProjectRoot shared.ProjectFinderFunc) string {
	if localPath := strings.TrimSpace(result.EnvConfig.SSHD.WorkspaceSync.LocalPath); localPath != "" {
		return localPath
	}
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return strings.TrimSpace(projectRoot)
		}
	}
	if !result.RemoteRepo() {
		return strings.TrimSpace(result.RepoPath)
	}
	return ""
}

// ResolveWorkspaceSyncParams resolves an environment to its pass or the precondition it fails.
func ResolveWorkspaceSyncParams(result config.OpenResult, findProjectRoot shared.ProjectFinderFunc) (WorkspaceSyncParams, error) {
	if !result.RemoteRepo() {
		return WorkspaceSyncParams{}, ErrWorkspaceSyncNotRemoteAgent
	}
	if !result.EnvConfig.SSHD.Enabled || !result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return WorkspaceSyncParams{}, ErrWorkspaceSyncNotEnabled
	}
	localPath := WorkspaceSyncLocalPath(result, findProjectRoot)
	if localPath == "" {
		return WorkspaceSyncParams{}, ErrWorkspaceSyncNoLocalPath
	}
	connection := open.SSHConnectionInfoForResult(result)
	return WorkspaceSyncParams{
		HostAlias:  connection.HostAlias,
		RemotePath: connection.WorkspacePath,
		LocalPath:  localPath,
	}, nil
}

// PreviewWorkspaceSync reports a pass's counts without writing anything.
func PreviewWorkspaceSync(ctx context.Context, params WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
	if err := validateWorkspaceSyncParams(&params); err != nil {
		return model.WorkspaceSyncResult{}, err
	}
	resolved, err := resolveWorkspaceSyncPaths(ctx, params)
	if err != nil {
		return model.WorkspaceSyncResult{}, err
	}
	if resolved.notGitRepo {
		return model.WorkspaceSyncResult{}, nil
	}
	remoteMeta := remoteWorkspaceFileMeta(ctx, params.HostAlias, params.RemotePath)
	toFetch := changedWorkspaceSyncPaths(resolved.remote, remoteMeta, resolved.localMeta)
	remote := pathSet(resolved.remote)
	deletions := 0
	for _, item := range sortedWorkspaceFileMetaKeys(resolved.localMeta) {
		if _, keep := remote[item]; keep {
			continue
		}
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		deletions++
	}
	// A missing outputs dir is normal before any cross-build.
	artifacts, _ := remoteOutputsFiles(ctx, params.HostAlias, core.DefaultRuntimeOutputsDir)
	artifactsLocal := filepath.Join(params.LocalPath, WorkspaceSyncArtifactsSubdir)
	artifactsToFetch := changedOutputsPaths(artifacts,
		remoteOutputsFileHashes(ctx, params.HostAlias, core.DefaultRuntimeOutputsDir, artifacts),
		localArtifactFileHashes(artifactsLocal, artifacts))
	return model.WorkspaceSyncResult{
		FilesCopied:     len(toFetch),
		FilesDeleted:    deletions,
		ArtifactsCopied: len(artifactsToFetch),
	}, nil
}
