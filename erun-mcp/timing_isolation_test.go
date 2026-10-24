package erunmcp

import (
	"fmt"
	"os"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// TestMain redirects timing records away from ~/.erun/timing, where test builds
// would evict real records; an explicitly set destination is kept.
func TestMain(m *testing.M) {
	var timingTempDir string
	if _, ok := os.LookupEnv(core.TimingRecordDirEnv); !ok {
		dir, err := os.MkdirTemp("", "erun-mcp-timing-test-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "create timing temp dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.Setenv(core.TimingRecordDirEnv, dir); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", core.TimingRecordDirEnv, err)
			os.Exit(1)
		}
		timingTempDir = dir
	}
	code := m.Run()
	if timingTempDir != "" {
		_ = os.RemoveAll(timingTempDir)
	}
	os.Exit(code)
}
