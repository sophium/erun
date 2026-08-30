package eruncommon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Workspace sync mirrors a remote-agent environment's pod worktree onto the
// host as plain files, one way. It lives here rather than in the desktop because
// every transport an operator or orchestrator reaches erun through — the CLI,
// the environment MCP, the desktop poller — needs the same pass, and a mirror
// that only the GUI can fill is a mirror an orchestrator cannot repair.

// WorkspaceSyncParams addresses one pass: the SSH host alias that reaches the
// pod, the worktree inside it, and the host directory that mirrors it.
type WorkspaceSyncParams struct {
	HostAlias  string
	RemotePath string
	LocalPath  string
}

// WorkspaceSyncResult is what one pass changed, and is what every transport
// reports back so the counts an operator sees are the same everywhere.
type WorkspaceSyncResult struct {
	FilesCopied     int
	FilesDeleted    int
	ArtifactsCopied int
}

// validateWorkspaceSyncParams trims the params in place and rejects a sync that
// is missing the host alias, remote path, or local path.
func validateWorkspaceSyncParams(params *WorkspaceSyncParams) error {
	params.HostAlias = strings.TrimSpace(params.HostAlias)
	params.RemotePath = strings.TrimSpace(params.RemotePath)
	params.LocalPath = strings.TrimSpace(params.LocalPath)
	if params.HostAlias == "" || params.RemotePath == "" || params.LocalPath == "" {
		return fmt.Errorf("host alias, remote path, and local path are required")
	}
	return nil
}

func SyncWorkspaceOnce(ctx context.Context, params WorkspaceSyncParams) (WorkspaceSyncResult, error) {
	if err := validateWorkspaceSyncParams(&params); err != nil {
		return WorkspaceSyncResult{}, err
	}
	pass := workspaceSyncPassLog{params: params, stale: "unknown"}
	defer func() { pass.emit() }()

	if err := EnsureLocalWorkspaceSyncTarget(params.LocalPath); err != nil {
		pass.failure = err
		return WorkspaceSyncResult{}, err
	}
	resolved, err := resolveWorkspaceSyncPaths(ctx, params)
	pass.recordResolved(resolved, err)
	if err != nil {
		return WorkspaceSyncResult{}, err
	}
	if resolved.notGitRepo {
		return WorkspaceSyncResult{}, nil
	}
	// Fetch only files whose size or mtime differs from the mirror, so a steady
	// state costs one metadata listing instead of re-transferring the whole tree
	// every pass; tar preserves mtime, so an unchanged file matches next pass.
	remoteMeta := remoteWorkspaceFileMeta(ctx, params.HostAlias, params.RemotePath)
	toFetch := changedWorkspaceSyncPaths(resolved.remote, remoteMeta, resolved.localMeta)
	pass.fetch = len(toFetch)
	// A fetch failure must NOT strand deletions: deletion correctness depends only
	// on the remote file listing, not on whether every changed file transferred.
	// Returning here on error let one un-fetchable file block every deletion, so
	// files removed in the pod lingered in the mirror forever. Record the error
	// and still run the delete + outputs steps.
	var fetchErr error
	if len(toFetch) > 0 {
		fetchErr = extractRemoteWorkspaceFiles(ctx, params.HostAlias, params.RemotePath, params.LocalPath, toFetch)
		pass.fetchErr = fetchErr
	}
	deleted, err := deleteLocalWorkspaceFilesNotInRemote(params.LocalPath, sortedWorkspaceFileMetaKeys(resolved.localMeta), resolved.remote)
	pass.deleted = deleted
	if err != nil {
		pass.deleteErr = err
		return WorkspaceSyncResult{}, err
	}
	artifacts, signing, err := syncOutputsArtifacts(ctx, params.HostAlias, DefaultRuntimeOutputsDir, filepath.Join(params.LocalPath, WorkspaceSyncArtifactsSubdir))
	pass.signed = signing.signed
	pass.signNote = signing.note
	if err != nil {
		pass.failure = err
		return WorkspaceSyncResult{}, err
	}
	result := WorkspaceSyncResult{FilesCopied: len(toFetch), FilesDeleted: deleted, ArtifactsCopied: artifacts}
	if fetchErr != nil {
		return result, fetchErr
	}
	return result, nil
}

// workspaceSyncPassLog is the always-on record of what one sync pass saw and
// did, emitted as a single bounded line. A mirror that kept adding files while
// silently never removing any took two investigations to explain precisely
// because a pass left no trace of its own inputs, so this is unconditional and
// counts-only — never one line per file.
type workspaceSyncPassLog struct {
	params     WorkspaceSyncParams
	notGitRepo bool
	remote     int
	stale      string
	local      int
	fetch      int
	deleted    int
	signed     int
	signNote   string
	fetchErr   error
	deleteErr  error
	failure    error
}

func (l *workspaceSyncPassLog) recordResolved(resolved workspaceSyncPaths, err error) {
	l.notGitRepo = resolved.notGitRepo
	l.remote = len(resolved.remote)
	l.stale = strconv.Itoa(resolved.stale)
	if resolved.staleUnknown {
		l.stale = "unknown"
	}
	l.local = len(resolved.localMeta)
	l.failure = err
}

func (l *workspaceSyncPassLog) emit() {
	log.Printf("erun: workspace sync %s -> %s: notGitRepo=%t remote=%d staleIndex=%s mirror=%d fetch=%d deleted=%d signed=%d%s%s%s%s",
		l.params.RemotePath, l.params.LocalPath, l.notGitRepo, l.remote, l.stale, l.local, l.fetch, l.deleted, l.signed,
		workspaceSyncPassNoteSuffix(" signNote", l.signNote),
		workspaceSyncPassErrorSuffix(" fetchError", l.fetchErr),
		workspaceSyncPassErrorSuffix(" deleteError", l.deleteErr),
		workspaceSyncPassErrorSuffix(" error", l.failure))
}

func workspaceSyncPassNoteSuffix(label, note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return label + "=" + strings.TrimSpace(note)
}

func workspaceSyncPassErrorSuffix(label string, err error) string {
	if err == nil {
		return ""
	}
	return label + "=" + strings.TrimSpace(err.Error())
}

// WorkspaceSyncArtifactsSubdir is the read-only subdir of the host mirror that
// receives the pod's $ERUN_OUTPUTS_DIR deliverables — e.g. a Windows .exe an
// agent cross-builds in the Linux pod. It sits beside the synced source but the
// source lane skips it (SafeWorkspaceSyncPath), so the two mirrors never contend
// over the same paths.
const WorkspaceSyncArtifactsSubdir = ".erun-outputs"

// workspaceSyncStagingSubdir is where a pass lands bytes that are still
// arriving. tar extracts in place, so extracting straight into the mirror let a
// reader open a file mid-write and get a prefix that looks complete — a
// truncated Mach-O reads as "not signed at all" rather than as truncated, which
// is evidence for the wrong conclusion. Staging then renaming publishes each
// file atomically, and keeps the only partial state in a directory that says so
// by name. It sits inside the lane it serves so the rename never crosses a
// filesystem, and both lanes skip it so staged bytes are never mistaken for
// mirror content.
const workspaceSyncStagingSubdir = ".erun-sync-staging"

// syncOutputsArtifacts mirrors the pod's deliverables directory
// (DefaultRuntimeOutputsDir) into artifactsLocal as a one-way,
// read-only host mirror. Artifacts live outside the git worktree, so they escape
// the gitignore that hides *.exe from the source mirror — this is how a Windows
// binary cross-built in the pod reaches the host to run/debug. Returns the number
// of artifact files delivered plus what ad-hoc signing did to them; a missing or
// empty outputs dir is a no-op.
func syncOutputsArtifacts(ctx context.Context, hostAlias, outputsRemote, artifactsLocal string) (int, hostArtifactSigningSummary, error) {
	var signing hostArtifactSigningSummary
	remote, err := remoteOutputsFiles(ctx, hostAlias, outputsRemote)
	if err != nil {
		return 0, signing, err
	}
	if len(remote) > 0 {
		if err := os.MkdirAll(artifactsLocal, 0o755); err != nil {
			return 0, signing, fmt.Errorf("create artifacts dir %s: %w", artifactsLocal, err)
		}
		// Clear the read-only bit set by the previous pass so the refreshed file
		// can replace it (matters on Windows, where a read-only attribute
		// otherwise blocks the rename onto it).
		if err := makeArtifactsWritable(artifactsLocal); err != nil {
			return 0, signing, err
		}
		if err := extractRemoteWorkspaceFiles(ctx, hostAlias, outputsRemote, artifactsLocal, remote); err != nil {
			return 0, signing, err
		}
		// The mirror is where a darwin artifact cross-built in the Linux pod first
		// becomes a file the operator can run, so it is where the signature macOS
		// demands has to come from. Sign while the files are still writable.
		signing = signHostArtifacts(localArtifactPaths(artifactsLocal, remote))
		if err := markArtifactsReadOnly(artifactsLocal, remote); err != nil {
			return 0, signing, err
		}
	}
	if err := pruneLocalArtifacts(artifactsLocal, remote); err != nil {
		return 0, signing, err
	}
	return len(remote), signing, nil
}

// localArtifactPaths resolves mirror-relative artifact paths against the mirror
// root, dropping any that do not stay inside it.
func localArtifactPaths(artifactsLocal string, paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, item := range paths {
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		resolved = append(resolved, filepath.Join(artifactsLocal, filepath.FromSlash(item)))
	}
	return resolved
}

// remoteOutputsDirAbsentExitCode is the sentinel the remote script in
// remoteOutputsFiles exits with when `cd` into the outputs dir fails, so that
// outcome can be told apart from every other way the listing can fail: ssh's
// own exit 255 on a connection failure, or `find` itself failing (e.g. on a
// permission error). Only this exact code means "confirmed absent"; nothing
// else does.
const remoteOutputsDirAbsentExitCode = 42

// remoteOutputsFiles lists the regular files under the pod outputs dir, relative
// to it. A not-yet-created dir yields no paths and no error, so the mirror is a
// no-op until an agent writes a deliverable. Any other failure — the ssh
// connection itself (exit 255), or `find` failing once inside the dir — is
// reported as an error rather than folded into an empty listing, so a caller
// that prunes local artifacts against this result never mistakes "could not
// tell" for "confirmed empty" (see ObservedHelmRelease for the same
// distinction applied to a helm read). GNU find's %P prints the path without
// the leading "./" so entries pass SafeWorkspaceSyncPath.
func remoteOutputsFiles(ctx context.Context, hostAlias, outputsRemote string) ([]string, error) {
	script := fmt.Sprintf("cd %s 2>/dev/null || exit %d; find . -type f -printf '%%P\\0'", shellQuote(outputsRemote), remoteOutputsDirAbsentExitCode)
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == remoteOutputsDirAbsentExitCode {
			return nil, nil
		}
		return nil, fmt.Errorf("list remote outputs: %w", err)
	}
	return parseWorkspaceSyncPathList(output), nil
}

// pruneLocalArtifacts deletes host artifact files no longer present in the pod so
// an artifact removed in the pod disappears from the host mirror too.
func pruneLocalArtifacts(artifactsLocal string, remotePaths []string) error {
	localPaths, err := ListLocalArtifactFiles(artifactsLocal)
	if err != nil {
		return err
	}
	_, err = deleteLocalWorkspaceFilesNotInRemote(artifactsLocal, localPaths, remotePaths)
	return err
}

// ListLocalArtifactFiles returns the regular files under root, relative to it and
// slash-normalized. A missing root yields no files, and the staging subdir is
// skipped so bytes still arriving are never offered as a deliverable.
func ListLocalArtifactFiles(root string) ([]string, error) {
	var files []string
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
		if info.IsDir() {
			if filepath.ToSlash(rel) == workspaceSyncStagingSubdir {
				return filepath.SkipDir
			}
			return nil
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
	files, err := ListLocalArtifactFiles(artifactsLocal)
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
// overwrite. An executable artifact stays executable: read-only is about who may
// edit the mirror, not about whether the operator can run what it delivered.
func markArtifactsReadOnly(artifactsLocal string, paths []string) error {
	for _, item := range paths {
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		full := filepath.Join(artifactsLocal, filepath.FromSlash(item))
		if err := os.Chmod(full, readOnlyArtifactMode(full)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("mark artifact read-only %s: %w", full, err)
		}
	}
	return nil
}

func readOnlyArtifactMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o111 != 0 {
		return 0o555
	}
	return 0o444
}

// workspaceSyncPaths is one pass's view of both sides of the mirror.
type workspaceSyncPaths struct {
	remote     []string
	localMeta  map[string]workspaceFileMeta
	notGitRepo bool
	// stale counts index entries the pod's worktree no longer has — the
	// deletions a pass exists to propagate, and the number that stayed
	// invisible while the mirror kept them. staleUnknown says the listing
	// itself failed, which reads the same as "none" without being told.
	stale        int
	staleUnknown bool
}

// resolveWorkspaceSyncPaths reports notGitRepo=true (with a nil error) when the
// remote is not a Git repository; callers treat that as a no-op sync pass.
func resolveWorkspaceSyncPaths(ctx context.Context, params WorkspaceSyncParams) (workspaceSyncPaths, error) {
	remotePaths, err := remoteWorkspaceGitVisibleFiles(ctx, params.HostAlias, params.RemotePath)
	if errors.Is(err, errRemoteNotGitRepo) {
		return workspaceSyncPaths{notGitRepo: true}, nil
	}
	if err != nil {
		return workspaceSyncPaths{}, err
	}
	// `git ls-files -c` reports the index, not the worktree, so a file the agent
	// removed without staging the removal keeps appearing as a remote file — and
	// the mirror, which deletes exactly what the remote listing omits, keeps it
	// forever. Subtracting the index entries whose file is gone is what makes a
	// deletion in the pod reach the host at all.
	missing, missingKnown := remoteWorkspaceMissingFiles(ctx, params.HostAlias, params.RemotePath)
	remotePaths = excludeWorkspaceSyncPaths(remotePaths, missing)
	// Symlinks (e.g. the per-module CLAUDE.md -> AGENTS.md pointers) cannot
	// round-trip to a plain-directory host mirror on Windows: their fingerprint
	// never matches, so they re-fetch every pass, and extracting them can fail —
	// which (before the delete step was decoupled from fetch) stranded every
	// deletion. Drop them from the synced set; AGENTS.md itself still syncs as a
	// regular file, so the mirror loses only the redundant pointer.
	remotePaths = excludeWorkspaceSyncPaths(remotePaths, remoteWorkspaceSymlinkSet(ctx, params.HostAlias, params.RemotePath))
	// The mirror is a one-way copy, so its file set comes from a plain directory
	// walk — the pod already applied the repo's ignore rules via
	// `git ls-files --exclude-standard`, so the host needs no git of its own.
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
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, "true")...)
	HideConsoleWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh workspace is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// EnsureLocalWorkspaceSyncTarget makes the mirror path usable as a sync target:
// it must be a directory, created if missing. The mirror is a one-way,
// read-only copy the sync owns, so it deliberately does NOT need to be a git
// worktree — sync reconciles files by listing the directory, not by local git.
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
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard", shellQuote(remotePath))
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(cmd)
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

// remoteWorkspaceMissingFiles returns the index entries whose file is no longer
// in the pod's worktree — what `git ls-files -c` keeps reporting after a plain
// `rm`. Best-effort: a listing failure degrades the pass to keeping those files
// rather than failing outright, and reports false so the per-pass diagnostic can
// say "unknown" instead of the "none missing" it would otherwise look like.
func remoteWorkspaceMissingFiles(ctx context.Context, hostAlias, remotePath string) (map[string]struct{}, bool) {
	script := fmt.Sprintf("cd %s && git ls-files -dz", shellQuote(remotePath))
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return pathSet(parseWorkspaceSyncPathList(output)), true
}

// remoteWorkspaceSymlinkSet returns the git-tracked symlink paths (mode 120000)
// in the remote worktree. Symlinks cannot round-trip to a plain-directory host
// mirror on Windows, so the sync excludes them (see resolveWorkspaceSyncPaths).
// Best-effort: a listing failure yields nil, so the sync degrades to keeping the
// symlinks rather than failing the pass.
func remoteWorkspaceSymlinkSet(ctx context.Context, hostAlias, remotePath string) map[string]struct{} {
	script := fmt.Sprintf("cd %s && git ls-files -sz --exclude-standard", shellQuote(remotePath))
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseGitLsFilesSymlinkPaths(output)
}

// parseGitLsFilesSymlinkPaths reads `git ls-files -sz` NUL-delimited records
// (`<mode> <object> <stage>\t<path>`) and returns the paths whose mode is the
// symlink mode 120000.
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

// excludeWorkspaceSyncPaths drops a set of paths from the git-visible list,
// preserving order. An empty set returns the input unchanged.
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
// (SafeWorkspaceSyncPath). A missing mirror yields an empty set.
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
			if relSlash == ".git" || relSlash == WorkspaceSyncArtifactsSubdir || relSlash == workspaceSyncStagingSubdir {
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

// remoteWorkspaceFileMeta fingerprints the pod's Git-visible files (size + mtime)
// so a pass can fetch only what changed. It is best-effort: any listing or parse
// failure yields no fingerprint for a path, and changedWorkspaceSyncPaths then
// fetches that path — so correctness never depends on the metadata, only the
// transfer volume does.
func remoteWorkspaceFileMeta(ctx context.Context, hostAlias, remotePath string) map[string]workspaceFileMeta {
	script := fmt.Sprintf("cd %s && git ls-files -coz --exclude-standard | xargs -0 -r stat -c '%%s %%Y %%n'", shellQuote(remotePath))
	cmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(cmd)
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
		if !SafeWorkspaceSyncPath(fields[2]) {
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

// extractRemoteWorkspaceFiles fetches paths from the pod into localPath. The
// archive lands in a staging dir and each file is renamed into place afterwards,
// so nothing reading the mirror can observe a half-written file; a pass that
// fails, is cancelled, or is killed leaves the mirror on its previous content.
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

// prepareWorkspaceSyncStaging gives the pass an empty staging dir beside the
// files it will publish, clearing whatever a killed pass left behind so staged
// bytes are only ever this pass's.
func prepareWorkspaceSyncStaging(localPath string) (string, error) {
	staging := filepath.Join(localPath, workspaceSyncStagingSubdir)
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear workspace sync staging %s: %w", staging, err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", fmt.Errorf("create workspace sync staging %s: %w", staging, err)
	}
	return staging, nil
}

// publishStagedWorkspaceFiles moves each staged entry onto its final path with
// rename(2), so a reader sees either the previous file or the complete new one.
// Symlinks publish the same way — filepath.Walk lstats, so a link moves as a
// link rather than being followed.
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

// streamRemoteWorkspaceArchive pipes the pod's tar of paths into an extract
// rooted at destination.
func streamRemoteWorkspaceArchive(ctx context.Context, hostAlias, remotePath, destination string, paths []string) error {
	script := fmt.Sprintf("cd %s && tar --null --ignore-failed-read -T - -cf -", shellQuote(remotePath))
	sshCmd := CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	HideConsoleWindow(sshCmd)
	sshCmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		return err
	}
	var sshStderr bytes.Buffer
	sshCmd.Stderr = &sshStderr

	tarCmd := CommandContext(ctx, "tar", "-xf", "-", "-C", destination)
	HideConsoleWindow(tarCmd)
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
	// Three subtrees a lane must never claim: the operator's own .git, the
	// artifact mirror the outputs lane owns, and the staging subdir, whose
	// contents are bytes still arriving rather than mirror content.
	for _, reserved := range []string{".git", WorkspaceSyncArtifactsSubdir, workspaceSyncStagingSubdir} {
		if cleaned == reserved || strings.HasPrefix(cleaned, reserved+"/") {
			return false
		}
	}
	return true
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
