package shared

import (
	"os"
	"testing"
)

func writeExecutableScript(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/stub.sh"
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}
