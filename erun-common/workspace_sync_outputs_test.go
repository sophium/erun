package eruncommon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive syncOutputsArtifacts against a stub `ssh` so the outputs
// listing and the prune it feeds are exercised together — erun#1657: an
// inconclusive listing (a dropped ssh connection, a `find` failure) must never
// be read as "no deliverables" and must never reach the prune, while a
// genuinely absent outputs dir and a genuinely empty one must still be
// no-ops, and a listing that succeeds must still prune what the pod removed.

// stubWorkspaceSyncSSHForOutputs points `ssh` at the TestMain stub with no
// source-lane listings configured, since these tests only exercise the
// outputs lane's own remote command.
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
	// No workspaceSyncStubOutputsEnv and no exit override: the stub's `cd`
	// branch behaves as it would on a pod that has never written a deliverable.

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

// TestRemoteOutputsFilesIncludesSSHStderrInsteadOfBareExitStatus is erun#1768:
// cmd.Output() already captures ssh's own diagnostic onto exitErr.Stderr, so a
// failure must surface it rather than the content-free "exit status 255" that
// %w alone renders.
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

// TestRemoteOutputsFilesReportsExitStatusPlainlyWhenSSHWroteNoStderr covers the
// case where ssh (or find) failed with nothing on stderr: the message must
// still name the exit status rather than emit an empty, misleading fragment.
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

// TestSyncOutputsArtifactsNeverTouchesAnUnchangedArtifactsMode is a regression
// test: delivery used to run the writable->extract->sign->read-only cycle
// unconditionally for every remote
// artifact on every pass, so an artifact whose content had not changed at all
// still passed through the 0644 window `makeArtifactsWritable` applies before
// re-extraction restores it -- on the majority of passes, per the issue's own
// sampling, since delivery ran every couple of seconds. An operator invoking
// the artifact directly from a shell during that window saw "permission
// denied" on an otherwise-correct binary.
//
// This locks the fix: when the remote fingerprint (size + mtime) matches what
// is already in the mirror, the artifact is never made writable, never
// re-extracted, and never re-marked read-only -- its mode is untouched start to
// finish. Deliberately configuring no tar archive stub proves this: on the old,
// unconditional code this test fails, because the pass still tries to
// re-fetch the "unchanged" artifact and the tar stub has nothing to serve.
func TestSyncOutputsArtifactsNeverTouchesAnUnchangedArtifactsMode(t *testing.T) {
	stubWorkspaceSyncSSH(t, nil, nil)

	artifactsLocal := t.TempDir()
	artifact := filepath.Join(artifactsLocal, "erun-darwin-arm64")
	if err := os.WriteFile(artifact, []byte("already built"), 0o644); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	// A settled mirror always ends a pass read-only+executable; a real artifact
	// between passes carries exactly this mode.
	if err := os.Chmod(artifact, 0o555); err != nil {
		t.Fatalf("chmod artifact read-only+executable: %v", err)
	}
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat seeded artifact: %v", err)
	}

	t.Setenv(workspaceSyncStubOutputsEnv, "erun-darwin-arm64")
	t.Setenv(workspaceSyncStubStatEnv, fmt.Sprintf("%d %d erun-darwin-arm64\n", info.Size(), info.ModTime().Unix()))
	// Deliberately no workspaceSyncStubArchiveEnv: an unchanged artifact must
	// never be re-fetched, so nothing here ever asks the tar stub for bytes.

	copied, _, err := syncOutputsArtifacts(context.Background(), "pod", "/home/agent/outputs", artifactsLocal)
	requireWorkspaceSyncNoError(t, err, "sync outputs artifacts for an unchanged artifact")
	if copied != 1 {
		t.Fatalf("expected 1 artifact reported present, got %d", copied)
	}

	after, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat artifact after sync: %v", err)
	}
	if after.Mode().Perm() != 0o555 {
		t.Fatalf("unchanged artifact's mode was touched: got %v, want 0o555 (executable never dropped)", after.Mode().Perm())
	}
}

func TestSyncOutputsArtifactsTreatsNonConnectionFindFailureAsInconclusive(t *testing.T) {
	stubWorkspaceSyncSSHForOutputs(t)
	// Exit 1 simulates `find` itself failing once inside the outputs dir (e.g. a
	// permission error) -- a real failure distinct from both success (0) and the
	// script's own "confirmed absent" sentinel (remoteOutputsDirAbsentExitCode).
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
