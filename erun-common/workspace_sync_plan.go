package eruncommon

import (
	"context"
	"errors"
	"strings"
)

// The distinct reasons a sync cannot run. They stay separate values because the
// operator's next move differs for each, and from the outside an empty mirror
// looks the same whichever one it is.
var (
	ErrWorkspaceSyncNotRemoteAgent = errors.New("only a remote-agent environment has a pod worktree to mirror")
	ErrWorkspaceSyncNotEnabled     = errors.New("workspace sync is not enabled for this environment")
	ErrWorkspaceSyncNoLocalPath    = errors.New("workspace sync has no local path to mirror into")
)

// WorkspaceSyncLocalPath resolves the host directory a pass mirrors into: the
// configured path first, then the project root, then the environment's own
// worktree when that already lives on this host.
func WorkspaceSyncLocalPath(result OpenResult, findProjectRoot ProjectFinderFunc) string {
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

// ResolveWorkspaceSyncParams turns an environment into the one pass it would
// run, or names the precondition it fails. Every transport resolves through
// here, so the desktop poller, the CLI and the MCP agree on both what a pass
// addresses and when there is nothing to address.
func ResolveWorkspaceSyncParams(result OpenResult, findProjectRoot ProjectFinderFunc) (WorkspaceSyncParams, error) {
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
	connection := SSHConnectionInfoForResult(result)
	return WorkspaceSyncParams{
		HostAlias:  connection.HostAlias,
		RemotePath: connection.WorkspacePath,
		LocalPath:  localPath,
	}, nil
}

// PreviewWorkspaceSync reports what a pass would change without changing it, so
// a dry run shows the counts the real pass would produce instead of a summary
// note. It reads the same remote listing and local walk the pass does and stops
// short of every write — no mirror directory is created, nothing is fetched, and
// nothing is deleted.
func PreviewWorkspaceSync(ctx context.Context, params WorkspaceSyncParams) (WorkspaceSyncResult, error) {
	if err := validateWorkspaceSyncParams(&params); err != nil {
		return WorkspaceSyncResult{}, err
	}
	resolved, err := resolveWorkspaceSyncPaths(ctx, params)
	if err != nil {
		return WorkspaceSyncResult{}, err
	}
	if resolved.notGitRepo {
		return WorkspaceSyncResult{}, nil
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
	// A missing or unreachable outputs dir is the normal case for an environment
	// that has cross-built nothing yet, so it reports zero rather than failing the
	// preview.
	artifacts, _ := remoteOutputsFiles(ctx, params.HostAlias, DefaultRuntimeOutputsDir)
	return WorkspaceSyncResult{
		FilesCopied:     len(toFetch),
		FilesDeleted:    deletions,
		ArtifactsCopied: len(artifacts),
	}, nil
}
