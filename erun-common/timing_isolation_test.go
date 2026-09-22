package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// The suite must leave the operator's real timing history byte-for-byte
// unchanged. A record this binary writes there is indistinguishable from a real
// build or deploy, and because retention prunes on write it is worse than
// additive: the fabricated record evicts a genuine one to stay inside the cap.
// This asserts the destination the binary actually resolves, so losing the
// TestMain redirect fails here rather than silently resuming the writes.
func TestTimingRecordsNeverResolveToTheOperatorsRealHistory(t *testing.T) {
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no ambient home directory to compare against: %v", err)
	}
	real := filepath.Join(home, ".erun", "timing")
	if filepath.Clean(dir) == filepath.Clean(real) {
		t.Fatalf("timing records resolve to the operator's real history %q; this binary must redirect %s at a temp tree",
			real, TimingRecordDirEnv)
	}
}

// Redirecting the destination must not be a way of turning the writer off.
// Given a directory, a finished timing root still produces its record there,
// with the run's own command and start time intact.
func TestWriteTimingRecordWritesToTheResolvedDirectory(t *testing.T) {
	dir := t.TempDir()
	previous := timingRecordDir
	timingRecordDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { timingRecordDir = previous })

	root := newStepTiming("deploy", nil)
	root.finish(nil)

	path, err := writeTimingRecord("deploy", root)
	if err != nil {
		t.Fatalf("writeTimingRecord: %v", err)
	}
	if got := filepath.Dir(path); got != dir {
		t.Fatalf("record written to %q, want it inside the resolved directory %q", got, dir)
	}
	record, err := loadTimingRecordFile(path)
	if err != nil {
		t.Fatalf("load written record: %v", err)
	}
	if record.Command != "deploy" {
		t.Fatalf("record command = %q, want deploy", record.Command)
	}
	if !record.StartedAt.Equal(root.snapshot().start) {
		t.Fatalf("record startedAt = %v, want the root step's start %v", record.StartedAt, root.snapshot().start)
	}
}
