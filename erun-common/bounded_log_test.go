package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testBoundedLogMaxBytes = 1024

func TestOpenBoundedAppendLogRotatesAnOvergrownLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", testBoundedLogMaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := OpenBoundedAppendLog(path, testBoundedLogMaxBytes, 0o644)
	if err != nil {
		t.Fatalf("open bounded append log: %v", err)
	}
	defer func() { _ = file.Close() }()

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("over-cap log was not rotated to .1: %v", err)
	}
	if len(rotated) != testBoundedLogMaxBytes+1 {
		t.Fatalf("rotated generation size = %d, want %d", len(rotated), testBoundedLogMaxBytes+1)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("live log size = %d after rotation, want 0", info.Size())
	}
}

func TestOpenBoundedAppendLogLeavesAnUnderCapLogAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.log")
	existing := "Forwarding from 127.0.0.1:17000 -> 17000\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := OpenBoundedAppendLog(path, testBoundedLogMaxBytes, 0o644)
	if err != nil {
		t.Fatalf("open bounded append log: %v", err)
	}
	if _, err := file.WriteString("Handling connection for 17000\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), existing) {
		t.Fatalf("under-cap log lost earlier content: %q", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("under-cap log must not rotate: stat .1 err = %v", err)
	}
}

// TestRotateOversizedLogReportsWhatItDid covers the exported re-check a caller
// holding an already-open log needs: OpenBoundedAppendLog hides the answer
// behind the open it performs, so a caller re-applying the cap to a long-lived
// writer has to be told whether anything actually moved.
func TestRotateOversizedLogReportsWhatItDid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forward.log")

	// Nothing there yet: nothing to rotate, and nothing reported.
	if RotateOversizedLog(path, testBoundedLogMaxBytes) {
		t.Error("expected no rotation for a log that does not exist")
	}

	// At the cap is left exactly as it is, so the run an operator opens the
	// file to read is still there.
	under := strings.Repeat("x", testBoundedLogMaxBytes)
	if err := os.WriteFile(path, []byte(under), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if RotateOversizedLog(path, testBoundedLogMaxBytes) {
		t.Error("expected no rotation at exactly the cap")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != under {
		t.Errorf("expected the under-cap log untouched, got %d bytes (err %v)", len(got), err)
	}

	// Over the cap rolls aside to the ".1" generation, and says so, because
	// the caller is the only one that can report it.
	if err := os.WriteFile(path, []byte(under+"y"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !RotateOversizedLog(path, testBoundedLogMaxBytes) {
		t.Error("expected an over-cap rotation to be reported")
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected the rolled generation beside the live log: %v", err)
	}
}
