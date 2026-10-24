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
