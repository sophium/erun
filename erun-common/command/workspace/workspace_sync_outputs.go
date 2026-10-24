package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/codesign"
)

// WorkspaceSyncArtifactsSubdir is the read-only host mirror of the pod's $ERUN_OUTPUTS_DIR;
// the source lane skips it.
const WorkspaceSyncArtifactsSubdir = ".erun-outputs"

// syncOutputsArtifacts mirrors pod deliverables, which live outside the git worktree,
// read-only to the host. Only refreshed artifacts are made writable, so an unchanged
// binary is never briefly non-executable.
func syncOutputsArtifacts(ctx context.Context, hostAlias, outputsRemote, artifactsLocal string) (int, codesign.HostArtifactSigningSummary, error) {
	var signing codesign.HostArtifactSigningSummary
	remote, err := remoteOutputsFiles(ctx, hostAlias, outputsRemote)
	if err != nil {
		return 0, signing, err
	}
	copied := 0
	if len(remote) > 0 {
		copied, signing, err = refreshOutputsArtifacts(ctx, hostAlias, outputsRemote, artifactsLocal, remote)
		if err != nil {
			return 0, signing, err
		}
	}
	if err := pruneLocalArtifacts(artifactsLocal, remote); err != nil {
		return 0, signing, err
	}
	return copied, signing, nil
}

func refreshOutputsArtifacts(ctx context.Context, hostAlias, outputsRemote, artifactsLocal string, remote []string) (int, codesign.HostArtifactSigningSummary, error) {
	var signing codesign.HostArtifactSigningSummary
	if err := os.MkdirAll(artifactsLocal, 0o755); err != nil {
		return 0, signing, fmt.Errorf("create artifacts dir %s: %w", artifactsLocal, err)
	}
	// Compare by content: a rebuilt identical output still bumps mtime.
	remoteHashes := remoteOutputsFileHashes(ctx, hostAlias, outputsRemote, remote)
	localHashes := localArtifactFileHashes(artifactsLocal, remote)
	toFetch := changedOutputsPaths(remote, remoteHashes, localHashes)
	if len(toFetch) > 0 {
		// Windows' read-only attribute blocks the rename onto the file.
		if err := makeArtifactsWritable(artifactsLocal, toFetch); err != nil {
			return 0, signing, err
		}
		if err := extractRemoteWorkspaceFiles(ctx, hostAlias, outputsRemote, artifactsLocal, toFetch); err != nil {
			return 0, signing, err
		}
	}
	// macOS requires a signature on darwin artifacts; sign while still writable.
	signing = codesign.SignHostArtifacts(localArtifactPaths(artifactsLocal, toFetch))
	if err := markArtifactsReadOnly(artifactsLocal, toFetch); err != nil {
		return 0, signing, err
	}
	return len(toFetch), signing, nil
}

// remoteOutputsFileHashes is best-effort: on failure every path counts as changed.
func remoteOutputsFileHashes(ctx context.Context, hostAlias, outputsRemote string, paths []string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	script := fmt.Sprintf("cd %s && xargs -0 -r sha256sum --zero", core.ShellQuote(outputsRemote))
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	cmd.Stdin = bytes.NewReader(encodeWorkspaceSyncPathList(paths))
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseOutputsFileHashes(output)
}

func parseOutputsFileHashes(output []byte) map[string]string {
	const hashLen = 64
	hashes := make(map[string]string)
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) < hashLen+2 || record[hashLen] != ' ' {
			continue
		}
		path := string(record[hashLen+2:])
		if !SafeWorkspaceSyncPath(path) {
			continue
		}
		hashes[path] = string(record[:hashLen])
	}
	return hashes
}

func localArtifactFileHashes(root string, paths []string) map[string]string {
	hashes := make(map[string]string, len(paths))
	for _, item := range paths {
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item)))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hashes[item] = hex.EncodeToString(sum[:])
	}
	return hashes
}

func changedOutputsPaths(remotePaths []string, remoteHashes, localHashes map[string]string) []string {
	changed := make([]string, 0, len(remotePaths))
	for _, path := range remotePaths {
		remoteHash, remoteKnown := remoteHashes[path]
		localHash, localKnown := localHashes[path]
		if !remoteKnown || !localKnown || remoteHash != localHash {
			changed = append(changed, path)
		}
	}
	return changed
}

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

// remoteOutputsDirAbsentExitCode alone means "confirmed absent"; ssh uses 255 and find
// failures differ.
const remoteOutputsDirAbsentExitCode = 42

// remoteOutputsFiles returns an error, not an empty list, when it cannot tell, so pruning
// never mistakes failure for empty. A missing dir is no paths and no error.
func remoteOutputsFiles(ctx context.Context, hostAlias, outputsRemote string) ([]string, error) {
	script := fmt.Sprintf("cd %s 2>/dev/null || exit %d; find . -type f -printf '%%P\\0'", core.ShellQuote(outputsRemote), remoteOutputsDirAbsentExitCode)
	cmd := core.CommandContext(ctx, "ssh", workspaceSyncSSHArgs(hostAlias, script)...)
	core.HideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == remoteOutputsDirAbsentExitCode {
			return nil, nil
		}
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return nil, fmt.Errorf("list remote outputs: %w: %s", err, stderr)
		}
		return nil, fmt.Errorf("list remote outputs: %w", err)
	}
	return parseWorkspaceSyncPathList(output), nil
}

func pruneLocalArtifacts(artifactsLocal string, remotePaths []string) error {
	localPaths, err := ListLocalArtifactFiles(artifactsLocal)
	if err != nil {
		return err
	}
	_, err = deleteLocalWorkspaceFilesNotInRemote(artifactsLocal, localPaths, remotePaths)
	return err
}

// ListLocalArtifactFiles skips the staging subdir so arriving bytes are never offered.
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
			if isWorkspaceSyncStagingPath(filepath.ToSlash(rel)) {
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

// makeArtifactsWritable touches only the listed paths.
func makeArtifactsWritable(artifactsLocal string, paths []string) error {
	for _, item := range paths {
		if !SafeWorkspaceSyncPath(item) {
			continue
		}
		full := filepath.Join(artifactsLocal, filepath.FromSlash(item))
		if err := os.Chmod(full, 0o644); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prepare artifact for refresh %s: %w", full, err)
		}
	}
	return nil
}

// markArtifactsReadOnly keeps executables executable.
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
