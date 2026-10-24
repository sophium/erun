package build

import (
	"os"
	"testing"
)

// failingBinaryPath writes a script that fails the test if executed.
func failingBinaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-must-not-run"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'kubectl must not run in this test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}
