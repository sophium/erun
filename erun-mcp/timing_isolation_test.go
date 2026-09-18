package erunmcp

import (
	"fmt"
	"os"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestMain keeps this binary's timing records out of the operator's real
// ~/.erun/timing history. The suite drives build paths that end in
// writeTimingRecord, so without this a `go test` run appends microsecond
// records for builds that never happened to the directory an operator reads to
// answer "how long does a build take"; retention prunes on write, so each one
// also evicts a genuine record. Redirected for the whole binary rather than per
// test, so a test added later is isolated by default. An explicitly set
// destination is left alone -- a caller who set one meant it.
func TestMain(m *testing.M) {
	var timingTempDir string
	if _, ok := os.LookupEnv(eruncommon.TimingRecordDirEnv); !ok {
		dir, err := os.MkdirTemp("", "erun-mcp-timing-test-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "create timing temp dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.Setenv(eruncommon.TimingRecordDirEnv, dir); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", eruncommon.TimingRecordDirEnv, err)
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
