package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const defaultWorkspaceSyncInterval = 2 * time.Second

type workspaceSyncParams struct {
	HostAlias  string
	RemotePath string
	LocalPath  string
}

type workspaceSyncResult struct {
	FilesCopied  int
	FilesDeleted int
}

type workspaceSyncWorker struct {
	cancel           context.CancelFunc
	status           workspaceSyncStatus
	lastErrorMessage string
}

type workspaceSyncStatus struct {
	Status     string
	Message    string
	LastSynced time.Time
	Files      int
}

func (a *App) startWorkspaceSyncForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !result.RemoteRepo() || !result.EnvConfig.SSHD.Enabled || !result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return
	}
	localPath := workspaceSyncLocalPath(result, a.deps.findProjectRoot)
	if localPath == "" {
		a.emitAppStatus(fmt.Sprintf("Workspace sync for %s/%s has no local path.", selection.Tenant, selection.Environment), false)
		return
	}
	key := selectionKey(selection)

	a.mu.Lock()
	if existing := a.workspaceSyncs[key]; existing != nil {
		a.mu.Unlock()
		return
	}
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	a.workspaceSyncs[key] = &workspaceSyncWorker{
		cancel: cancel,
		status: workspaceSyncStatus{Status: "starting", Message: "Starting workspace sync"},
	}
	a.mu.Unlock()

	a.emitAppStatus(fmt.Sprintf("Starting workspace sync for %s/%s...", selection.Tenant, selection.Environment), false)
	go a.runWorkspaceSyncLoop(ctx, key, selection, result, localPath)
}

func (a *App) reconcileWorkspaceSyncForSelection(selection uiSelection, enabled bool) {
	a.stopWorkspaceSyncForSelection(selection)
	if enabled {
		a.startWorkspaceSyncForSelection(selection)
	}
}

func (a *App) stopWorkspaceSyncForSelection(selection uiSelection) {
	key := selectionKey(selection)
	a.mu.Lock()
	worker := a.workspaceSyncs[key]
	if worker != nil {
		delete(a.workspaceSyncs, key)
	}
	a.mu.Unlock()
	if worker != nil && worker.cancel != nil {
		worker.cancel()
	}
}

func (a *App) runWorkspaceSyncLoop(ctx context.Context, key string, selection uiSelection, result eruncommon.OpenResult, localPath string) {
	params := workspaceSyncParams{
		HostAlias:  eruncommon.SSHConnectionInfoForResult(result).HostAlias,
		RemotePath: eruncommon.SSHConnectionInfoForResult(result).WorkspacePath,
		LocalPath:  localPath,
	}
	for {
		a.setWorkspaceSyncStatus(key, workspaceSyncStatus{Status: "syncing", Message: "Syncing workspace"})
		switch a.prepareWorkspaceSyncPass(ctx, key, result, params) {
		case workspaceSyncPassStop:
			return
		case workspaceSyncPassRetry:
			continue
		}
		synced, err := a.deps.syncWorkspace(ctx, params)
		if err != nil {
			a.setWorkspaceSyncError(key, err)
		} else {
			a.setWorkspaceSyncStatus(key, workspaceSyncStatus{
				Status:     "synced",
				Message:    fmt.Sprintf("Synced %d files, deleted %d", synced.FilesCopied, synced.FilesDeleted),
				LastSynced: time.Now(),
				Files:      synced.FilesCopied,
			})
		}
		if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
			return
		}
	}
}

// workspaceSyncPassOutcome tells runWorkspaceSyncLoop how to proceed after the
// pre-sync readiness checks.
type workspaceSyncPassOutcome int

const (
	// workspaceSyncPassProceed means the prerequisites are satisfied and the
	// loop should run the sync.
	workspaceSyncPassProceed workspaceSyncPassOutcome = iota
	// workspaceSyncPassRetry means a prerequisite was not ready (the loop should
	// continue to the next iteration after the interval has already elapsed).
	workspaceSyncPassRetry
	// workspaceSyncPassStop means the context was cancelled while waiting (the
	// loop should return).
	workspaceSyncPassStop
)

// prepareWorkspaceSyncPass runs the per-iteration readiness checks for the sync
// loop: it ensures SSHD when the local port cannot be reached and waits for the
// remote SSHD to answer. On any failure it records the worker status, sleeps one
// interval, and reports whether the loop should retry or stop; otherwise it
// reports proceed. Extracted so runWorkspaceSyncLoop stays under the cyclomatic
// limit; the guard order, status writes, and sleep-driven continue/return
// semantics are unchanged.
func (a *App) prepareWorkspaceSyncPass(ctx context.Context, key string, result eruncommon.OpenResult, params workspaceSyncParams) workspaceSyncPassOutcome {
	if a.deps.canConnectLocalPort != nil && !a.deps.canConnectLocalPort(eruncommon.SSHLocalPortForResult(result)) && a.deps.ensureSSHD != nil {
		if err := a.deps.ensureSSHD(ctx, result); err != nil {
			a.setWorkspaceSyncError(key, err)
			if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
				return workspaceSyncPassStop
			}
			return workspaceSyncPassRetry
		}
	}
	if a.deps.workspaceSyncReady != nil {
		if err := a.deps.workspaceSyncReady(ctx, params.HostAlias); err != nil {
			a.setWorkspaceSyncStatus(key, workspaceSyncStatus{Status: "starting", Message: "Waiting for SSHD"})
			if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
				return workspaceSyncPassStop
			}
			return workspaceSyncPassRetry
		}
	}
	return workspaceSyncPassProceed
}

func sleepWorkspaceSyncInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *App) setWorkspaceSyncError(key string, err error) {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "unknown error"
	}
	shouldEmit := false
	a.mu.Lock()
	if worker := a.workspaceSyncs[key]; worker != nil {
		shouldEmit = worker.lastErrorMessage != message
		worker.lastErrorMessage = message
		worker.status = workspaceSyncStatus{Status: "error", Message: message}
	}
	a.mu.Unlock()
	if shouldEmit {
		a.emitAppStatus("Workspace sync failed: "+message, false)
	}
}

func (a *App) setWorkspaceSyncStatus(key string, status workspaceSyncStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if worker := a.workspaceSyncs[key]; worker != nil {
		worker.status = status
		if status.Status == "synced" {
			worker.lastErrorMessage = ""
		}
	}
}

func (a *App) workspaceSyncStatus(selection uiSelection) workspaceSyncStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if worker := a.workspaceSyncs[selectionKey(selection)]; worker != nil {
		return worker.status
	}
	return workspaceSyncStatus{Status: "stopped"}
}

func (a *App) stopAllWorkspaceSyncsLocked() {
	for key, worker := range a.workspaceSyncs {
		if worker != nil && worker.cancel != nil {
			worker.cancel()
		}
		delete(a.workspaceSyncs, key)
	}
}

func workspaceSyncLocalPath(result eruncommon.OpenResult, findProjectRoot eruncommon.ProjectFinderFunc) string {
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

func syncWorkspaceOnce(ctx context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
	params.HostAlias = strings.TrimSpace(params.HostAlias)
	params.RemotePath = strings.TrimSpace(params.RemotePath)
	params.LocalPath = strings.TrimSpace(params.LocalPath)
	if params.HostAlias == "" || params.RemotePath == "" || params.LocalPath == "" {
		return workspaceSyncResult{}, fmt.Errorf("host alias, remote path, and local path are required")
	}
	if err := ensureLocalWorkspaceSyncTarget(ctx, params.LocalPath); err != nil {
		return workspaceSyncResult{}, err
	}
	remotePaths, localPaths, notGitRepo, err := resolveWorkspaceSyncPaths(ctx, params)
	if err != nil {
		return workspaceSyncResult{}, err
	}
	if notGitRepo {
		return workspaceSyncResult{}, nil
	}
	if len(remotePaths) > 0 {
		if err := extractRemoteWorkspaceFiles(ctx, params.HostAlias, params.RemotePath, params.LocalPath, remotePaths); err != nil {
			return workspaceSyncResult{}, err
		}
	}
	deleted, err := deleteLocalWorkspaceFilesNotInRemote(params.LocalPath, localPaths, remotePaths)
	if err != nil {
		return workspaceSyncResult{}, err
	}
	return workspaceSyncResult{FilesCopied: len(remotePaths), FilesDeleted: deleted}, nil
}

// resolveWorkspaceSyncPaths lists the Git-visible files on the remote and local
// sides for one sync pass, applying the local ignore filter to the remote set.
// notGitRepo is true (with a nil error) when the remote workspace is not a Git
// repository, which syncWorkspaceOnce treats as a no-op. Extracted so
// syncWorkspaceOnce stays under the cyclomatic limit; the listing and filtering
// order is unchanged.
func resolveWorkspaceSyncPaths(ctx context.Context, params workspaceSyncParams) (remotePaths, localPaths []string, notGitRepo bool, err error) {
	remotePaths, err = remoteWorkspaceGitVisibleFiles(ctx, params.HostAlias, params.RemotePath)
	if errors.Is(err, errRemoteNotGitRepo) {
		return nil, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	localPaths, err = localWorkspaceGitVisibleFiles(ctx, params.LocalPath)
	if err != nil {
		return nil, nil, false, err
	}
	remotePaths, err = filterLocalIgnoredWorkspaceSyncPaths(ctx, params.LocalPath, remotePaths)
	if err != nil {
		return nil, nil, false, err
	}
	return remotePaths, localPaths, false, nil
}

func workspaceSyncSSHReady(ctx context.Context, hostAlias string) error {
	hostAlias = strings.TrimSpace(hostAlias)
	if hostAlias == "" {
		return fmt.Errorf("ssh host alias is required")
	}
	cmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, "true")...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh workspace is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureLocalWorkspaceSyncTarget(ctx context.Context, localPath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("local workspace path %s: %w", localPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local workspace path is not a directory: %s", localPath)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", localPath, "rev-parse", "--is-inside-work-tree")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("local workspace is not a Git worktree: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

var errRemoteNotGitRepo = errors.New("remote workspace is not a git repository")

func remoteWorkspaceGitVisibleFiles(ctx context.Context, hostAlias, remotePath string) ([]string, error) {
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard", shellQuote(remotePath))
	output, err := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...).CombinedOutput()
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

func localWorkspaceGitVisibleFiles(ctx context.Context, localPath string) ([]string, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", localPath, "ls-files", "-coz", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("list local Git-visible files: %w", err)
	}
	return parseWorkspaceSyncPathList(output), nil
}

func filterLocalIgnoredWorkspaceSyncPaths(ctx context.Context, localPath string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", localPath, "check-ignore", "-z", "--stdin")
	cmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return paths, nil
		}
		return nil, fmt.Errorf("check local ignored files: %w", err)
	}
	ignored := pathSet(parseWorkspaceSyncPathList(output))
	filtered := make([]string, 0, len(paths))
	for _, item := range paths {
		if _, skip := ignored[item]; !skip {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func extractRemoteWorkspaceFiles(ctx context.Context, hostAlias, remotePath, localPath string, paths []string) error {
	script := fmt.Sprintf("cd %s && tar --null --ignore-failed-read -T - -cf -", shellQuote(remotePath))
	sshCmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	sshCmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		return err
	}
	var sshStderr bytes.Buffer
	sshCmd.Stderr = &sshStderr

	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", localPath)
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
		if !safeWorkspaceSyncPath(item) {
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
		if !safeWorkspaceSyncPath(path) {
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
		if !safeWorkspaceSyncPath(item) {
			continue
		}
		output.WriteString(item)
		output.WriteByte(0)
	}
	return output.Bytes()
}

func safeWorkspaceSyncPath(value string) bool {
	value = filepath.ToSlash(value)
	if value == "" || value == "." || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := pathClean(value)
	if cleaned != value || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return false
	}
	return cleaned != ".git" && !strings.HasPrefix(cleaned, ".git/")
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
