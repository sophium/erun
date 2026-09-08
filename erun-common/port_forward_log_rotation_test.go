package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRotateOversizedFileTruncatesInPlaceForStillOpenAppendWriter is the
// regression test for erun#2161: a kubectl port-forward's log fd is opened
// O_APPEND and stays open for the forward's whole life, so rotation must bound
// the file without that process ever reopening it. Before this fix, nothing
// checked the file's size at all, so a long-lived forward's log grew without
// limit (449MB observed on one host). This test fails without RotateOversizedFile
// (the file would just keep growing past maxBytes) and passes with it.
func TestRotateOversizedFileTruncatesInPlaceForStillOpenAppendWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forward.log")
	const maxBytes = 1024

	writer := openAppendWriter(t, path)
	defer func() {
		_ = writer.Close()
	}()

	// Simulate kubectl having already written past the cap through its own
	// still-open fd.
	overCap := bytes.Repeat([]byte("x"), maxBytes+512)
	if _, err := writer.Write(overCap); err != nil {
		t.Fatalf("seed oversized content: %v", err)
	}

	rotated, err := RotateOversizedFile(path, maxBytes)
	if err != nil {
		t.Fatalf("RotateOversizedFile: %v", err)
	}
	if !rotated {
		t.Fatalf("expected rotation to report true for an oversized file")
	}

	// The writer's fd is still open and never reopened the file -- exactly
	// the shape a detached kubectl process has. Its next write must land at
	// the fresh end-of-file rather than growing the file further.
	if _, err := writer.Write([]byte("more")); err != nil {
		t.Fatalf("post-rotation write: %v", err)
	}

	assertFileContent(t, path, "more")
	assertFileSize(t, path+".1", len(overCap))
}

func openAppendWriter(t *testing.T, path string) *os.File {
	t.Helper()
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open log for append: %v", err)
	}
	return writer
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("expected %s to hold %q, got %q (%d bytes)", path, want, data, len(data))
	}
}

func assertFileSize(t *testing.T, path string, want int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) != want {
		t.Fatalf("expected %s to hold %d bytes, got %d", path, want, len(data))
	}
}

// TestRotateOversizedFileReclaimsAnAlreadyOversizedLogWithNoActiveWriter
// covers the other half of erun#2161: existing 449MB-shaped logs need
// reclaiming too, not just bounding future growth. No writer needs to be
// open for this case -- a plain oversized file on disk from before this fix
// existed.
func TestRotateOversizedFileReclaimsAnAlreadyOversizedLogWithNoActiveWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forward.log")
	const maxBytes = 1024

	if err := os.WriteFile(path, make([]byte, maxBytes*10), 0o644); err != nil {
		t.Fatalf("seed oversized file: %v", err)
	}

	rotated, err := RotateOversizedFile(path, maxBytes)
	if err != nil {
		t.Fatalf("RotateOversizedFile: %v", err)
	}
	if !rotated {
		t.Fatalf("expected rotation to report true")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat canonical path: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected the canonical log truncated to empty, got %d bytes", info.Size())
	}
	backupInfo, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if backupInfo.Size() != maxBytes*10 {
		t.Fatalf("expected the backup to preserve the original %d bytes, got %d", maxBytes*10, backupInfo.Size())
	}
}

// TestRotateOversizedFileLeavesASmallFileUntouched pins the negative case so
// rotation never runs (and never creates a .1 backup) for the ordinary,
// bounded log.
func TestRotateOversizedFileLeavesASmallFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forward.log")
	const maxBytes = 1024

	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatalf("seed small file: %v", err)
	}

	rotated, err := RotateOversizedFile(path, maxBytes)
	if err != nil {
		t.Fatalf("RotateOversizedFile: %v", err)
	}
	if rotated {
		t.Fatalf("expected no rotation for a file under the cap")
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup to be created, stat err = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "small" {
		t.Fatalf("expected file content unchanged, got %q", data)
	}
}

// TestRotateOversizedFileMissingPathIsANoOp mirrors the "no forward yet"
// ordinary case: nothing has ever written a log at this path.
func TestRotateOversizedFileMissingPathIsANoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never-written.log")

	rotated, err := RotateOversizedFile(path, 1024)
	if err != nil {
		t.Fatalf("expected no error for a missing file, got %v", err)
	}
	if rotated {
		t.Fatalf("expected no rotation for a missing file")
	}
}
