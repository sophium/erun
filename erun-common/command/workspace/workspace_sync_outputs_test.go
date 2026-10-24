package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An inconclusive outputs listing must never reach the prune.

func stubWorkspaceSyncSSHForOutputs(t *testing.T) {
	t.Helper()
	stubWorkspaceSyncSSH(t, nil, nil)
}

func TestSyncOutputsArtifactsDoesNotPruneOnSSHConnectionFailure(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	t.Setenv(workspaceSyncStubOutputsExitEnv, "255")

	artifactsLocal := t.TempDir()
	seedWorkspaceMirror(t, artifactsLocal, "keep.exe")

	_, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	if err == nil {
		t.Fatal("expected an ssh connection failure (exit 255) to surface as an error")
	}
	if _, statErr := os.Stat(filepath.Join(artifactsLocal, "keep.exe")); statErr != nil {
		t.Fatalf("an inconclusive listing must leave the artifact mirror alone, got: %v", statErr)
	}
}

func TestSyncOutputsArtifactsTreatsAbsentOutputsDirAsEmptyNoop(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	// No outputs env and no exit override: the pod never wrote a deliverable.

	artifactsLocal := filepath.Join(t.TempDir(), "artifacts-not-yet-created")

	copied, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "sync outputs artifacts")
	if copied != 0 {
		t.Fatalf("expected 0 artifacts copied for an absent outputs dir, got %d", copied)
	}
	if _, statErr := os.Stat(artifactsLocal); !os.IsNotExist(statErr) {
		t.Fatalf("expected no mirror directory to be created for an absent outputs dir, got: %v", statErr)
	}
}

func TestSyncOutputsArtifactsStillPrunesRemovedArtifactOnSuccessfulListing(t *testing.T) {
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"keep.exe": []byte("still built")})
	stubWorkspaceSyncSSHForOutputs(t)
	t.Setenv(workspaceSyncStubOutputsEnv, "keep.exe")
	t.Setenv(workspaceSyncStubArchiveEnv, archive)

	artifactsLocal := t.TempDir()
	seedWorkspaceMirror(t, artifactsLocal, "gone.exe")

	copied, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "sync outputs artifacts")
	if copied != 1 {
		t.Fatalf("expected 1 artifact copied, got %d", copied)
	}
	if _, statErr := os.Stat(filepath.Join(artifactsLocal, "gone.exe")); !os.IsNotExist(statErr) {
		t.Fatalf("expected the pod-removed artifact to still be pruned on a successful listing, got: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(artifactsLocal, "keep.exe")); statErr != nil {
		t.Fatalf("expected the newly listed artifact to be present: %v", statErr)
	}
}

func TestRemoteOutputsFilesIncludesSSHStderrInsteadOfBareExitStatus(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	t.Setenv(workspaceSyncStubOutputsExitEnv, "255")
	t.Setenv(workspaceSyncStubOutputsStderrEnv, "ssh: connect to host pod port 22: Connection refused")

	_, err := remoteOutputsFiles(context.Background(), "pod", "/home/agent/outputs")
	if err == nil {
		t.Fatal("expected the ssh failure to surface as an error")
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("error %q does not include ssh's own diagnostic", err.Error())
	}
}

func TestRemoteOutputsFilesReportsExitStatusPlainlyWhenSSHWroteNoStderr(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	t.Setenv(workspaceSyncStubOutputsExitEnv, "255")

	_, err := remoteOutputsFiles(context.Background(), "pod", "/home/agent/outputs")
	if err == nil {
		t.Fatal("expected the ssh failure to surface as an error")
	}
	if !strings.Contains(err.Error(), "exit status 255") {
		t.Fatalf("error %q should still name the exit status when ssh wrote nothing to stderr", err.Error())
	}
}

// The archive stub is only consulted if the pass decides to transfer.
func TestSyncOutputsArtifactsNeverTouchesAnUnchangedArtifactsMode(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)

	artifactsLocal := t.TempDir()
	artifact := filepath.Join(artifactsLocal, "erun-darwin-arm64")
	if err := os.WriteFile(artifact, []byte("already built"), 0o644); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if err := os.Chmod(artifact, 0o555); err != nil {
		t.Fatalf("chmod artifact read-only+executable: %v", err)
	}

	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"erun-darwin-arm64": []byte("already built")})
	t.Setenv(workspaceSyncStubArchiveEnv, archive)
	t.Setenv(workspaceSyncStubOutputsEnv, "erun-darwin-arm64")

	copied, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "sync outputs artifacts for an unchanged artifact")
	if copied != 0 {
		t.Fatalf("expected 0 artifacts transferred for unchanged content, got %d", copied)
	}

	after, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat artifact after sync: %v", err)
	}
	if after.Mode().Perm() != 0o555 {
		t.Fatalf("unchanged artifact's mode was touched: got %v, want 0o555 (executable never dropped)", after.Mode().Perm())
	}
}

// workspaceSyncStubFetchMarkerEnv marks the actual transfer, not just the reported count.
func TestSyncOutputsArtifactsSkipsUnchangedContentOnSecondPass(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	t.Setenv(workspaceSyncStubOutputsEnv, "bin/tool")
	marker := filepath.Join(t.TempDir(), "fetched")
	t.Setenv(workspaceSyncStubFetchMarkerEnv, marker)

	artifactsLocal := t.TempDir()
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"bin/tool": []byte("build output v1")})
	t.Setenv(workspaceSyncStubArchiveEnv, archive)

	copied, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "first sync pass")
	if copied != 1 {
		t.Fatalf("first pass: copied = %d, want 1 (nothing mirrored yet)", copied)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("first pass should have transferred the file: %v", statErr)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("clear transfer marker: %v", err)
	}

	copied, _, err = syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "second sync pass")
	if copied != 0 {
		t.Fatalf("second pass: copied = %d, want 0 for unchanged content", copied)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("second pass re-transferred unchanged content instead of skipping it")
	}

	changedArchive := writeWorkspaceSyncArchive(t, map[string][]byte{"bin/tool": []byte("build output v2, genuinely different")})
	t.Setenv(workspaceSyncStubArchiveEnv, changedArchive)

	copied, _, err = syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "third sync pass")
	if copied != 1 {
		t.Fatalf("third pass: copied = %d, want 1 for genuinely changed content", copied)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("third pass should have transferred the genuinely changed content: %v", statErr)
	}
}

func TestSyncOutputsArtifactsTreatsNonConnectionFindFailureAsInconclusive(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	// Exit 1: find failed inside the dir, distinct from the absent sentinel.
	t.Setenv(workspaceSyncStubOutputsExitEnv, "1")

	artifactsLocal := t.TempDir()
	seedWorkspaceMirror(t, artifactsLocal, "keep.exe")

	_, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	if err == nil {
		t.Fatal("expected a non-connection find failure to surface as an error, not an empty listing")
	}
	if _, statErr := os.Stat(filepath.Join(artifactsLocal, "keep.exe")); statErr != nil {
		t.Fatalf("an inconclusive listing must leave the artifact mirror alone, got: %v", statErr)
	}
}
