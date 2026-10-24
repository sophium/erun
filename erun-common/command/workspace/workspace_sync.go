package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Workspace sync mirrors a pod worktree onto the host, one way. It lives here so every
// transport (CLI, MCP, desktop) runs the same pass.

// WorkspaceSyncParams addresses one sync pass.
type WorkspaceSyncParams struct {
	HostAlias  string
	RemotePath string
	LocalPath  string
}

func validateWorkspaceSyncParams(params *WorkspaceSyncParams) error {
	params.HostAlias = strings.TrimSpace(params.HostAlias)
	params.RemotePath = strings.TrimSpace(params.RemotePath)
	params.LocalPath = strings.TrimSpace(params.LocalPath)
	if params.HostAlias == "" || params.RemotePath == "" || params.LocalPath == "" {
		return fmt.Errorf("host alias, remote path, and local path are required")
	}
	return nil
}

func SyncWorkspaceOnce(ctx context.Context, params WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
	if err := validateWorkspaceSyncParams(&params); err != nil {
		return model.WorkspaceSyncResult{}, err
	}
	pass := workspaceSyncPassLog{params: params}
	defer func() { workspaceSyncPasses.record(&pass) }()

	if err := EnsureLocalWorkspaceSyncTarget(params.LocalPath); err != nil {
		pass.failure = err
		return model.WorkspaceSyncResult{}, err
	}
	resolved, err := resolveWorkspaceSyncPaths(ctx, params)
	pass.recordResolved(resolved, err)
	if err != nil {
		return model.WorkspaceSyncResult{}, err
	}
	if resolved.notGitRepo {
		return model.WorkspaceSyncResult{}, nil
	}
	// Fetch only files whose size or mtime differs; tar preserves mtime.
	remoteMeta := remoteWorkspaceFileMeta(ctx, params.HostAlias, params.RemotePath)
	toFetch := changedWorkspaceSyncPaths(resolved.remote, remoteMeta, resolved.localMeta)
	pass.fetch = len(toFetch)
	// A fetch failure must not block deletions, which depend only on the remote listing.
	var fetchErr error
	if len(toFetch) > 0 {
		fetchErr = extractRemoteWorkspaceFiles(ctx, params.HostAlias, params.RemotePath, params.LocalPath, toFetch)
		pass.fetchErr = fetchErr
	}
	deleted, err := deleteLocalWorkspaceFilesNotInRemote(params.LocalPath, sortedWorkspaceFileMetaKeys(resolved.localMeta), resolved.remote)
	pass.deleted = deleted
	if err != nil {
		pass.deleteErr = err
		return model.WorkspaceSyncResult{}, err
	}
	artifacts, signing, err := syncOutputsArtifacts(ctx, params.HostAlias, core.DefaultRuntimeOutputsDir, filepath.Join(params.LocalPath, WorkspaceSyncArtifactsSubdir))
	pass.signed = signing.Signed
	pass.signNote = signing.Note
	if err != nil {
		pass.failure = err
		return model.WorkspaceSyncResult{}, err
	}
	result := model.WorkspaceSyncResult{FilesCopied: len(toFetch), FilesDeleted: deleted, ArtifactsCopied: artifacts}
	if fetchErr != nil {
		return result, fetchErr
	}
	return result, nil
}

// workspaceSyncStagingSubdir holds bytes still arriving; files are renamed into the
// mirror so readers never see a partial file. It lives inside the lane so rename stays
// on one filesystem, and both lanes skip it.
const workspaceSyncStagingSubdir = ".erun-sync-staging"

type workspaceSyncPaths struct {
	remote     []string
	localMeta  map[string]workspaceFileMeta
	notGitRepo bool
	// stale counts index entries the worktree no longer has; staleUnknown means the listing failed.
	stale        int
	staleUnknown bool
}

// resolveWorkspaceSyncPaths reports notGitRepo=true with a nil error when the remote
// is not a Git repository.
func resolveWorkspaceSyncPaths(ctx context.Context, params WorkspaceSyncParams) (workspaceSyncPaths, error) {
	remotePaths, err := remoteWorkspaceGitVisibleFiles(ctx, params.HostAlias, params.RemotePath)
	if errors.Is(err, errRemoteNotGitRepo) {
		return workspaceSyncPaths{notGitRepo: true}, nil
	}
	if err != nil {
		return workspaceSyncPaths{}, err
	}
	// `git ls-files -c` lists the index, so subtract entries whose file is gone or an
	// unstaged deletion never reaches the mirror.
	missing, missingKnown := remoteWorkspaceMissingFiles(ctx, params.HostAlias, params.RemotePath)
	remotePaths = excludeWorkspaceSyncPaths(remotePaths, missing)
	// Symlinks cannot round-trip to a Windows plain-directory mirror; their targets still sync.
	remotePaths = excludeWorkspaceSyncPaths(remotePaths, remoteWorkspaceSymlinkSet(ctx, params.HostAlias, params.RemotePath))
	// The pod already applied ignore rules, so the host needs no git.
	localMeta, err := localWorkspaceSourceFileMeta(params.LocalPath)
	if err != nil {
		return workspaceSyncPaths{}, err
	}
	return workspaceSyncPaths{
		remote:       remotePaths,
		localMeta:    localMeta,
		stale:        len(missing),
		staleUnknown: !missingKnown,
	}, nil
}

func WorkspaceSyncSSHReady(ctx context.Context, hostAlias string) error {
	hostAlias = strings.TrimSpace(hostAlias)
	if hostAlias == "" {
		return fmt.Errorf("ssh host alias is required")
	}
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, "true")...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh workspace is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// EnsureLocalWorkspaceSyncTarget ensures the mirror path is a directory; it need not be a git worktree.
func EnsureLocalWorkspaceSyncTarget(localPath string) error {
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		return fmt.Errorf("create local workspace path %s: %w", localPath, err)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("local workspace path %s: %w", localPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local workspace path is not a directory: %s", localPath)
	}
	return nil
}

var errRemoteNotGitRepo = errors.New("remote workspace is not a git repository")

func remoteWorkspaceGitVisibleFiles(ctx context.Context, hostAlias, remotePath string) ([]string, error) {
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard", core.ShellQuote(remotePath))
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if strings.Contains(detail, "not a git repository") {
			return nil, errRemoteNotGitRepo
		}
		if detail == "" {
			return nil, fmt.Errorf("list remote Git-visible files: %w", err)
		}
		return nil, fmt.Errorf("list remote Git-visible files: %w: %s", err, detail)
	}
	return parseWorkspaceSyncPathList(output), nil
}

// remoteWorkspaceMissingFiles returns index entries missing from the worktree. Best-effort:
// false means the listing failed.
func remoteWorkspaceMissingFiles(ctx context.Context, hostAlias, remotePath string) (map[string]struct{}, bool) {
	script := fmt.Sprintf("cd %s && git ls-files -dz", core.ShellQuote(remotePath))
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return pathSet(parseWorkspaceSyncPathList(output)), true
}

// remoteWorkspaceSymlinkSet returns tracked symlink paths; nil on failure.
func remoteWorkspaceSymlinkSet(ctx context.Context, hostAlias, remotePath string) map[string]struct{} {
	script := fmt.Sprintf("cd %s && git ls-files -sz --exclude-standard", core.ShellQuote(remotePath))
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseGitLsFilesSymlinkPaths(output)
}

func parseGitLsFilesSymlinkPaths(output []byte) map[string]struct{} {
	set := make(map[string]struct{})
	for _, record := range bytes.Split(output, []byte{0}) {
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			continue
		}
		path := string(record[tab+1:])
		if bytes.HasPrefix(record, []byte("120000 ")) && SafeWorkspaceSyncPath(path) {
			set[path] = struct{}{}
		}
	}
	return set
}

func excludeWorkspaceSyncPaths(paths []string, excluded map[string]struct{}) []string {
	if len(excluded) == 0 {
		return paths
	}
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, drop := excluded[p]; drop {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// workspaceFileMeta is a file's size and mtime fingerprint.
type workspaceFileMeta struct {
	Size  int64
	MTime int64
}

// localWorkspaceSourceFileMeta walks the mirror's source lane, skipping .git and .erun-outputs.
func localWorkspaceSourceFileMeta(root string) (map[string]workspaceFileMeta, error) {
	meta := make(map[string]workspaceFileMeta)
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		relSlash := filepath.ToSlash(rel)
		if info.IsDir() {
			if relSlash == ".git" || relSlash == WorkspaceSyncArtifactsSubdir || isWorkspaceSyncStagingPath(relSlash) {
				return filepath.SkipDir
			}
			return nil
		}
		if SafeWorkspaceSyncPath(relSlash) {
			meta[relSlash] = workspaceFileMeta{Size: info.Size(), MTime: info.ModTime().Unix()}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// remoteWorkspaceFileMeta is best-effort: a path with no fingerprint is simply fetched.
func remoteWorkspaceFileMeta(ctx context.Context, hostAlias, remotePath string) map[string]workspaceFileMeta {
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard | xargs -0 -r stat -c '%%s %%Y %%n'", core.ShellQuote(remotePath))
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWorkspaceFileMeta(string(output))
}

// parseWorkspaceFileMeta takes the path as the remainder after two spaces, so paths may contain spaces.
func parseWorkspaceFileMeta(output string) map[string]workspaceFileMeta {
	meta := make(map[string]workspaceFileMeta)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			continue
		}
		size, sizeErr := strconv.ParseInt(fields[0], 10, 64)
		mtime, mtimeErr := strconv.ParseInt(fields[1], 10, 64)
		if sizeErr != nil || mtimeErr != nil {
			continue
		}
		if !SafeWorkspaceSyncPath(fields[2]) {
			continue
		}
		meta[fields[2]] = workspaceFileMeta{Size: size, MTime: mtime}
	}
	return meta
}

// changedWorkspaceSyncPaths fetches anything missing, changed, or of unknown fingerprint.
func changedWorkspaceSyncPaths(remotePaths []string, remoteMeta, localMeta map[string]workspaceFileMeta) []string {
	changed := make([]string, 0, len(remotePaths))
	for _, path := range remotePaths {
		remote, remoteKnown := remoteMeta[path]
		local, localKnown := localMeta[path]
		if !remoteKnown || !localKnown || remote != local {
			changed = append(changed, path)
		}
	}
	return changed
}

func sortedWorkspaceFileMetaKeys(meta map[string]workspaceFileMeta) []string {
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// extractRemoteWorkspaceFiles stages the archive then renames each file, so a failed
// pass leaves the mirror on its previous content.
func extractRemoteWorkspaceFiles(ctx context.Context, hostAlias, remotePath, localPath string, paths []string) error {
	staging, err := prepareWorkspaceSyncStaging(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := streamRemoteWorkspaceArchive(ctx, hostAlias, remotePath, staging, paths); err != nil {
		return err
	}
	return publishStagedWorkspaceFiles(staging, localPath)
}

// workspaceSyncStagingStaleAge separates a killed pass's debris from a slow live one.
const workspaceSyncStagingStaleAge = time.Hour

// prepareWorkspaceSyncStaging uses a unique dir per pass so concurrent passes on one
// mirror (desktop poller and `erun sshd sync`) never destroy each other's extraction.
func prepareWorkspaceSyncStaging(localPath string) (string, error) {
	sweepStaleWorkspaceSyncStagingDirs(localPath)
	staging, err := os.MkdirTemp(localPath, workspaceSyncStagingSubdir+"-")
	if err != nil {
		return "", fmt.Errorf("create workspace sync staging in %s: %w", localPath, err)
	}
	return staging, nil
}

// sweepStaleWorkspaceSyncStagingDirs is best-effort: leftover staging dirs cost disk, not correctness.
func sweepStaleWorkspaceSyncStagingDirs(localPath string) {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), workspaceSyncStagingSubdir) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < workspaceSyncStagingStaleAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(localPath, entry.Name()))
	}
}

// publishStagedWorkspaceFiles renames each entry so readers see the old or complete new file;
// Walk lstats, so symlinks move as links.
func publishStagedWorkspaceFiles(staging, localPath string) error {
	return filepath.Walk(staging, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(staging, p)
		if relErr != nil {
			return relErr
		}
		destination := filepath.Join(localPath, rel)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return fmt.Errorf("create mirror directory for %s: %w", rel, err)
		}
		if err := os.Rename(p, destination); err != nil {
			return fmt.Errorf("publish %s into the mirror: %w", rel, err)
		}
		return nil
	})
}

func streamRemoteWorkspaceArchive(ctx context.Context, hostAlias, remotePath, destination string, paths []string) error {
	script := fmt.Sprintf("cd %s && tar --null --ignore-failed-read -T - -cf -", core.ShellQuote(remotePath))
	sshCmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(sshCmd)
	sshCmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		return err
	}
	var sshStderr bytes.Buffer
	sshCmd.Stderr = &sshStderr

	tarCmd := core.CommandContext(ctx, "tar", "-xf", "-", "-C", destination)
	core.HideConsoleWindow(tarCmd)
	tarCmd.Stdin = sshStdout
	var tarStderr bytes.Buffer
	tarCmd.Stderr = &tarStderr

	if err := sshCmd.Start(); err != nil {
		return fmt.Errorf("start remote archive: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = sshCmd.Wait()
		return fmt.Errorf("start local archive extract: %w", err)
	}
	tarErr := tarCmd.Wait()
	sshErr := sshCmd.Wait()
	if sshErr != nil {
		return fmt.Errorf("create remote archive: %w: %s", sshErr, strings.TrimSpace(sshStderr.String()))
	}
	if tarErr != nil {
		return fmt.Errorf("extract remote archive: %w: %s", tarErr, strings.TrimSpace(tarStderr.String()))
	}
	return nil
}

func workspaceSyncSSHArgs(hostAlias, remoteCommand string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		strings.TrimSpace(hostAlias),
		remoteCommand,
	}
}

func deleteLocalWorkspaceFilesNotInRemote(localPath string, localPaths, remotePaths []string) (int, error) {
	remote := pathSet(remotePaths)
	deleted := 0
	dirs := make([]string, 0)
	for _, item := range localPaths {
		if _, exists := remote[item]; exists {
			continue
		}
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		fullPath := filepath.Join(localPath, filepath.FromSlash(item))
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
		dirs = append(dirs, filepath.Dir(fullPath))
	}
	removeEmptyWorkspaceSyncDirs(localPath, dirs)
	return deleted, nil
}

func removeEmptyWorkspaceSyncDirs(localPath string, dirs []string) {
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		if dir == localPath || !strings.HasPrefix(dir, localPath) {
			continue
		}
		_ = os.Remove(dir)
	}
}

func parseWorkspaceSyncPathList(output []byte) []string {
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		path := string(part)
		if !SafeWorkspaceSyncPath(path) {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func encodeWorkspaceSyncPathList(paths []string) []byte {
	var output bytes.Buffer
	for _, item := range paths {
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		output.WriteString(item)
		output.WriteByte(0)
	}
	return output.Bytes()
}

func SafeWorkspaceSyncPath(value string) bool {
	value = filepath.ToSlash(value)
	if value == "" || value == "." || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := pathClean(value)
	if cleaned != value || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return false
	}
	return !isReservedWorkspaceSyncPath(cleaned)
}

// isReservedWorkspaceSyncPath matches .git, the outputs mirror, and staging dirs (by prefix,
// since they carry a random suffix).
func isReservedWorkspaceSyncPath(cleaned string) bool {
	if isWorkspaceSyncStagingPath(cleaned) {
		return true
	}
	for _, reserved := range []string{".git", WorkspaceSyncArtifactsSubdir} {
		if cleaned == reserved || strings.HasPrefix(cleaned, reserved+"/") {
			return true
		}
	}
	return false
}

func isWorkspaceSyncStagingPath(cleaned string) bool {
	first := cleaned
	if idx := strings.IndexByte(cleaned, '/'); idx >= 0 {
		first = cleaned[:idx]
	}
	return strings.HasPrefix(first, workspaceSyncStagingSubdir)
}

func pathClean(value string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))), "./")
}

func pathSet(paths []string) map[string]struct{} {
	result := make(map[string]struct{}, len(paths))
	for _, item := range paths {
		result[item] = struct{}{}
	}
	return result
}
