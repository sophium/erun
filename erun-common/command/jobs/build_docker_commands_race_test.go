package jobs

import (
	"os"
	"testing"
)

// writeExecutableScript writes body as an executable shell script in a fresh
// temp dir and returns its path, so ERUN_<NAME>_BIN can redirect a real
// command to a stub without depending on any live toolchain.
func writeExecutableScript(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/stub.sh"
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}
