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
	"strconv"
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
	FilesCopied     int
	FilesDeleted    int
	ArtifactsCopied int
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

// startWorkspaceSyncForConfiguredEnvs starts the host-mirror sync poller for
// every configured remote-agent env that has SSHD and workspace sync enabled,
// so an orchestrator's linked mirrors populate and stay live without the
// operator opening each env's tab first. Called at startup: without it, sync
// only ran for envs opened this session, so a linked-but-unopened env's mirror
// stayed empty. startWorkspaceSyncForSelection re-validates each env and dedups
// an already-running poller, so this is safe to call for every env and
// idempotent if an env is later opened.
func (a *App) startWorkspaceSyncForConfiguredEnvs() {
	if a.deps.store == nil {
		return
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return
	}
	for _, tenant := range tenants {
		envs, envErr := a.deps.store.ListEnvConfigs(tenant.Name)
		if envErr != nil {
			continue
		}
		for _, env := range envs {
			// Cheap pre-filter; startWorkspaceSyncForSelection re-validates and
			// gates on RemoteRepo(), so a non-remote env here is a no-op.
			if !env.SSHD.Enabled || !env.SSHD.WorkspaceSync.Enabled {
				continue
			}
			a.startWorkspaceSyncForSelection(uiSelection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
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
			message := fmt.Sprintf("Synced %d files, deleted %d", synced.FilesCopied, synced.FilesDeleted)
			if synced.ArtifactsCopied > 0 {
				message += fmt.Sprintf(", %d artifacts", synced.ArtifactsCopied)
			}
			a.setWorkspaceSyncStatus(key, workspaceSyncStatus{
				Status:     "synced",
				Message:    message,
				LastSynced: time.Now(),
				Files:      synced.FilesCopied,
			})
		}
		if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
			return
		}
	}
}

type workspaceSyncPassOutcome int

const (
	workspaceSyncPassProceed workspaceSyncPassOutcome = iota
	workspaceSyncPassRetry
	workspaceSyncPassStop
)

// prepareWorkspaceSyncPass sleeps one interval itself before signalling retry, so
// the sync loop's continue does not busy-spin on an unreachable SSHD.
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
	if err := ensureLocalWorkspaceSyncTarget(params.LocalPath); err != nil {
		return workspaceSyncResult{}, err
	}
	remotePaths, localMeta, notGitRepo, err := resolveWorkspaceSyncPaths(ctx, params)
	if err != nil {
		return workspaceSyncResult{}, err
	}
	if notGitRepo {
		return workspaceSyncResult{}, nil
	}
	// Fetch only files whose size or mtime differs from the mirror, so a steady
	// state costs one metadata listing instead of re-transferring the whole tree
	// every pass; tar preserves mtime, so an unchanged file matches next pass.
	remoteMeta := remoteWorkspaceFileMeta(ctx, params.HostAlias, params.RemotePath)
	toFetch := changedWorkspaceSyncPaths(remotePaths, remoteMeta, localMeta)
	if len(toFetch) > 0 {
		if err := extractRemoteWorkspaceFiles(ctx, params.HostAlias, params.RemotePath, params.LocalPath, toFetch); err != nil {
			return workspaceSyncResult{}, err
		}
	}
	deleted, err := deleteLocalWorkspaceFilesNotInRemote(params.LocalPath, sortedWorkspaceFileMetaKeys(localMeta), remotePaths)
	if err != nil {
		return workspaceSyncResult{}, err
	}
	artifacts, err := syncOutputsArtifacts(ctx, params.HostAlias, eruncommon.DefaultRuntimeOutputsDir, filepath.Join(params.LocalPath, workspaceSyncArtifactsSubdir))
	if err != nil {
		return workspaceSyncResult{}, err
	}
	return workspaceSyncResult{FilesCopied: len(toFetch), FilesDeleted: deleted, ArtifactsCopied: artifacts}, nil
}

// workspaceSyncArtifactsSubdir is the read-only subdir of the host mirror that
// receives the pod's $ERUN_OUTPUTS_DIR deliverables — e.g. a Windows .exe an
// agent cross-builds in the Linux pod. It sits beside the synced source but the
// source lane skips it (safeWorkspaceSyncPath), so the two mirrors never contend
// over the same paths.
const workspaceSyncArtifactsSubdir = ".erun-outputs"

// syncOutputsArtifacts mirrors the pod's deliverables directory
// (eruncommon.DefaultRuntimeOutputsDir) into artifactsLocal as a one-way,
// read-only host mirror. Artifacts live outside the git worktree, so they escape
// the gitignore that hides *.exe from the source mirror — this is how a Windows
// binary cross-built in the pod reaches the host to run/debug. Returns the number
// of artifact files delivered; a missing or empty outputs dir is a no-op.
func syncOutputsArtifacts(ctx context.Context, hostAlias, outputsRemote, artifactsLocal string) (int, error) {
	remote, err := remoteOutputsFiles(ctx, hostAlias, outputsRemote)
	if err != nil {
		return 0, err
	}
	if len(remote) > 0 {
		if err := os.MkdirAll(artifactsLocal, 0o755); err != nil {
			return 0, fmt.Errorf("create artifacts dir %s: %w", artifactsLocal, err)
		}
		// Clear the read-only bit set by the previous pass so the tar extract can
		// overwrite the mirror in place (matters on Windows, where a read-only
		// attribute otherwise blocks the rewrite).
		if err := makeArtifactsWritable(artifactsLocal); err != nil {
			return 0, err
		}
		if err := extractRemoteWorkspaceFiles(ctx, hostAlias, outputsRemote, artifactsLocal, remote); err != nil {
			return 0, err
		}
		if err := markArtifactsReadOnly(artifactsLocal, remote); err != nil {
			return 0, err
		}
	}
	if err := pruneLocalArtifacts(artifactsLocal, remote); err != nil {
		return 0, err
	}
	return len(remote), nil
}

// remoteOutputsFiles lists the regular files under the pod outputs dir, relative
// to it. A not-yet-created dir yields no paths and no error, so the mirror is a
// no-op until an agent writes a deliverable. GNU find's %P prints the path
// without the leading "./" so entries pass safeWorkspaceSyncPath.
func remoteOutputsFiles(ctx context.Context, hostAlias, outputsRemote string) ([]string, error) {
	script := fmt.Sprintf("cd %s 2>/dev/null && find . -type f -printf '%%P\\0'", shellQuote(outputsRemote))
	cmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	eruncommon.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		// `cd` into a not-yet-created outputs dir exits non-zero; treat the whole
		// "no deliverables yet" case as an empty, error-free listing.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil
		}
		return nil, fmt.Errorf("list remote outputs: %w", err)
	}
	return parseWorkspaceSyncPathList(output), nil
}

// pruneLocalArtifacts deletes host artifact files no longer present in the pod so
// an artifact removed in the pod disappears from the host mirror too.
func pruneLocalArtifacts(artifactsLocal string, remotePaths []string) error {
	localPaths, err := listLocalArtifactFiles(artifactsLocal)
	if err != nil {
		return err
	}
	_, err = deleteLocalWorkspaceFilesNotInRemote(artifactsLocal, localPaths, remotePaths)
	return err
}

// listLocalArtifactFiles returns the regular files under root, relative to it and
// slash-normalized. A missing root yields no files.
func listLocalArtifactFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// makeArtifactsWritable restores the write bit on mirrored artifact files so the
// next sync pass can overwrite them; markArtifactsReadOnly re-applies read-only
// after the refresh. Directories stay writable throughout.
func makeArtifactsWritable(artifactsLocal string) error {
	files, err := listLocalArtifactFiles(artifactsLocal)
	if err != nil {
		return err
	}
	for _, item := range files {
		full := filepath.Join(artifactsLocal, filepath.FromSlash(item))
		if err := os.Chmod(full, 0o644); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prepare artifact for refresh %s: %w", full, err)
		}
	}
	return nil
}

// markArtifactsReadOnly strips the write bit from mirrored artifact files so the
// host copy reads as the read-only mirror it is (on Windows this sets the
// read-only attribute), signalling the operator not to edit a copy the sync will
// overwrite.
func markArtifactsReadOnly(artifactsLocal string, paths []string) error {
	for _, item := range paths {
		if !safeWorkspaceSyncPath(item) {
			continue
		}
		full := filepath.Join(artifactsLocal, filepath.FromSlash(item))
		if err := os.Chmod(full, 0o444); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("mark artifact read-only %s: %w", full, err)
		}
	}
	return nil
}

// resolveWorkspaceSyncPaths reports notGitRepo=true (with a nil error) when the
// remote is not a Git repository; callers treat that as a no-op sync pass.
func resolveWorkspaceSyncPaths(ctx context.Context, params workspaceSyncParams) (remotePaths []string, localMeta map[string]workspaceFileMeta, notGitRepo bool, err error) {
	remotePaths, err = remoteWorkspaceGitVisibleFiles(ctx, params.HostAlias, params.RemotePath)
	if errors.Is(err, errRemoteNotGitRepo) {
		return nil, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	// The mirror is a one-way copy, so its file set comes from a plain directory
	// walk — the pod already applied the repo's ignore rules via
	// `git ls-files --exclude-standard`, so the host needs no git of its own.
	localMeta, err = localWorkspaceSourceFileMeta(params.LocalPath)
	if err != nil {
		return nil, nil, false, err
	}
	return remotePaths, localMeta, false, nil
}

func workspaceSyncSSHReady(ctx context.Context, hostAlias string) error {
	hostAlias = strings.TrimSpace(hostAlias)
	if hostAlias == "" {
		return fmt.Errorf("ssh host alias is required")
	}
	cmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, "true")...)
	eruncommon.HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh workspace is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ensureLocalWorkspaceSyncTarget makes the mirror path usable as a sync target:
// it must be a directory, created if missing. The mirror is a one-way,
// read-only copy the sync owns, so it deliberately does NOT need to be a git
// worktree — sync reconciles files by listing the directory, not by local git.
func ensureLocalWorkspaceSyncTarget(localPath string) error {
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
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard", shellQuote(remotePath))
	cmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	eruncommon.HideConsoleWindow(cmd)
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

// workspaceFileMeta is the change-detection fingerprint for one mirrored file:
// its size and mtime (unix seconds). tar preserves mtime on extract, so a file
// left unchanged in the pod keeps the same fingerprint on the host and is
// skipped on the next pass.
type workspaceFileMeta struct {
	Size  int64
	MTime int64
}

// localWorkspaceSourceFileMeta fingerprints the mirror's source-lane files with
// a plain filesystem walk, so the host copy needs no local git. It skips the
// operator's own .git (never synced or pruned) and the outputs lane's
// .erun-outputs subdir, keeping only the paths the source lane owns
// (safeWorkspaceSyncPath). A missing mirror yields an empty set.
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
			if relSlash == ".git" || relSlash == workspaceSyncArtifactsSubdir {
				return filepath.SkipDir
			}
			return nil
		}
		if safeWorkspaceSyncPath(relSlash) {
			meta[relSlash] = workspaceFileMeta{Size: info.Size(), MTime: info.ModTime().Unix()}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// remoteWorkspaceFileMeta fingerprints the pod's Git-visible files (size + mtime)
// so a pass can fetch only what changed. It is best-effort: any listing or parse
// failure yields no fingerprint for a path, and changedWorkspaceSyncPaths then
// fetches that path — so correctness never depends on the metadata, only the
// transfer volume does.
func remoteWorkspaceFileMeta(ctx context.Context, hostAlias, remotePath string) map[string]workspaceFileMeta {
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard | xargs -0 -r stat -c '%%s %%Y %%n'", shellQuote(remotePath))
	cmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	eruncommon.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWorkspaceFileMeta(string(output))
}

// parseWorkspaceFileMeta reads `stat -c '%s %Y %n'` lines (size, mtime, path).
// The path is the remainder after the first two spaces, so paths with spaces
// parse correctly; an unparseable line is skipped (its path is then fetched).
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
		if !safeWorkspaceSyncPath(fields[2]) {
			continue
		}
		meta[fields[2]] = workspaceFileMeta{Size: size, MTime: mtime}
	}
	return meta
}

// changedWorkspaceSyncPaths returns, in remotePaths order, the paths a pass must
// fetch: those missing locally, changed (size or mtime differ), or whose
// fingerprint is unknown on either side (fetch when unsure).
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

func extractRemoteWorkspaceFiles(ctx context.Context, hostAlias, remotePath, localPath string, paths []string) error {
	script := fmt.Sprintf("cd %s && tar --null --ignore-failed-read -T - -cf -", shellQuote(remotePath))
	sshCmd := exec.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	eruncommon.HideConsoleWindow(sshCmd)
	eruncommon.BoundCommandWait(sshCmd)
	sshCmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		return err
	}
	var sshStderr bytes.Buffer
	sshCmd.Stderr = &sshStderr

	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", localPath)
	eruncommon.HideConsoleWindow(tarCmd)
	eruncommon.BoundCommandWait(tarCmd)
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
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") {
		return false
	}
	// The source lane must ignore the artifact mirror subdir so it never prunes
	// or lists files the outputs lane owns.
	return cleaned != workspaceSyncArtifactsSubdir && !strings.HasPrefix(cleaned, workspaceSyncArtifactsSubdir+"/")
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
