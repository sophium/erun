package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// Retention prunes on write, so a stray record would evict a genuine one.
func TestTimingRecordsNeverResolveToTheOperatorsRealHistory(t *testing.T) {
	dir, err := core.TimingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no ambient home directory to compare against: %v", err)
	}
	realDir := filepath.Join(home, ".erun", "timing")
	if filepath.Clean(dir) == filepath.Clean(realDir) {
		t.Fatalf("timing records resolve to the operator's real history %q; this binary must redirect %s at a temp tree",
			realDir, core.TimingRecordDirEnv)
	}
}

func TestWriteTimingRecordWritesToTheResolvedDirectory(t *testing.T) {
	dir := t.TempDir()
	previous := core.TimingRecordDir
	core.TimingRecordDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { core.TimingRecordDir = previous })

	root := core.NewStepTiming("deploy", nil)
	root.Finish(nil)

	path, err := core.WriteTimingRecord("deploy", root)
	if err != nil {
		t.Fatalf("writeTimingRecord: %v", err)
	}
	if got := filepath.Dir(path); got != dir {
		t.Fatalf("record written to %q, want it inside the resolved directory %q", got, dir)
	}
	record, err := LoadTimingRecordFile(path)
	if err != nil {
		t.Fatalf("load written record: %v", err)
	}
	if record.Command != "deploy" {
		t.Fatalf("record command = %q, want deploy", record.Command)
	}
	if !record.StartedAt.Equal(root.Snapshot().Start) {
		t.Fatalf("record startedAt = %v, want the root step's start %v", record.StartedAt, root.Snapshot().Start)
	}
}
