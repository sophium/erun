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

// WorkspaceSyncArtifactsSubdir is the read-only subdir of the host mirror that
// receives the pod's $ERUN_OUTPUTS_DIR deliverables — e.g. a Windows .exe an
// agent cross-builds in the Linux pod. It sits beside the synced source but the
// source lane skips it (SafeWorkspaceSyncPath), so the two mirrors never contend
// over the same paths.
const WorkspaceSyncArtifactsSubdir = ".erun-outputs"

// syncOutputsArtifacts mirrors the pod's deliverables directory
// (DefaultRuntimeOutputsDir) into artifactsLocal as a one-way,
// read-only host mirror. Artifacts live outside the git worktree, so they escape
// the gitignore that hides *.exe from the source mirror — this is how a Windows
// binary cross-built in the pod reaches the host to run/debug. Returns the number
// of artifact files actually transferred this pass plus what ad-hoc signing did
// to them; a missing or empty outputs dir is a no-op, and a pass whose content is
// unchanged since the last one transfers nothing.
//
// Only the artifacts being refreshed this pass go through the
// writable->extract->sign->read-only cycle: the cycle used to run
// unconditionally for every remote artifact on every pass, so an artifact whose
// content had not changed in weeks still spent most of its time at the 0644 mode
// `makeArtifactsWritable` applies before the re-extract restores it — an operator
// invoking it directly from a shell during that window saw "permission denied"
// on an otherwise-correct binary.
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
	// Fetch only what actually changed by content: outputs are agent
	// deliverables, and an agent can rewrite one byte-for-byte identical to
	// what is already mirrored (e.g. rerunning a cross-compile) — that still
	// bumps mtime, the same effect a Docker COPY has on a build context, so
	// mtime cannot tell "rewritten" from "identical" the way it can for the
	// source lane's own tar-preserved fetch.
	remoteHashes := remoteOutputsFileHashes(ctx, hostAlias, outputsRemote, remote)
	localHashes := localArtifactFileHashes(artifactsLocal, remote)
	toFetch := changedOutputsPaths(remote, remoteHashes, localHashes)
	if len(toFetch) > 0 {
		// Clear the read-only bit set by the previous pass so the refreshed file
		// can replace it (matters on Windows, where a read-only attribute
		// otherwise blocks the rename onto it). Only the paths being refreshed
		// are touched, so an unchanged artifact never passes through this mode.
		if err := makeArtifactsWritable(artifactsLocal, toFetch); err != nil {
			return 0, signing, err
		}
		if err := extractRemoteWorkspaceFiles(ctx, hostAlias, outputsRemote, artifactsLocal, toFetch); err != nil {
			return 0, signing, err
		}
	}
	// The mirror is where a darwin artifact cross-built in the Linux pod first
	// becomes a file the operator can run, so it is where the signature macOS
	// demands has to come from. Sign while the files are still writable.
	signing = codesign.SignHostArtifacts(localArtifactPaths(artifactsLocal, toFetch))
	if err := markArtifactsReadOnly(artifactsLocal, toFetch); err != nil {
		return 0, signing, err
	}
	return len(toFetch), signing, nil
}

// remoteOutputsFileHashes fingerprints the given pod outputs files by content
// (sha256) rather than size or mtime, for the reason in syncOutputsArtifacts's
// comment above. Best-effort like remoteWorkspaceFileMeta: any failure yields no
// fingerprints, so changedOutputsPaths degrades to treating every path as
// changed rather than trusting a read that did not happen.
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

// parseOutputsFileHashes reads `sha256sum --zero` NUL-terminated records
// (`<64-hex-hash><space><text/binary flag><filename>`). A malformed record is
// skipped, which folds into remoteOutputsFileHashes's best-effort contract.
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

// localArtifactFileHashes reads and hashes only the given mirror-relative
// paths, so a path the mirror does not have yet (a first-ever pass, or one
// pruned since) is simply absent from the result rather than an error;
// changedOutputsPaths then treats an absent local hash as changed.
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

// changedOutputsPaths returns, in remotePaths order, the paths a pass must
// fetch: those missing locally, changed (content hash differs), or whose
// fingerprint is unknown on either side (fetch when unsure).
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

// makeArtifactsWritable restores the write bit on the given mirrored artifact
// files so this pass can overwrite them; markArtifactsReadOnly re-applies
// read-only after the refresh. Only the listed paths are touched — an artifact
// not being refreshed this pass must never spend time at this mode.
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
