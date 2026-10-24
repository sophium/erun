package eruncommon

import (
	"os"
	"testing"
	"time"
)

// heavyDualStreamScript is a shell one-liner that writes well over 64KB (the
// default pipe buffer size, and the default bufio.Scanner token size) on both
// stdout and stderr. It exists to reproduce a full pipe blocking the writing
// child forever once the parent stops draining it, so a regression here must
// show up as a test that hangs rather than one that merely fails an
// assertion.
const heavyDualStreamScript = `for i in $(seq 1 20000); do
  echo "out-$i padding padding padding padding padding padding padding padding"
  echo "err-$i padding padding padding padding padding padding padding padding" 1>&2
done`

// runWithHangGuard runs fn on its own goroutine and fails the test if it does
// not return within timeout, instead of letting a real hang wedge the whole
// test binary forever.
func runWithHangGuard(t *testing.T, timeout time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		t.Fatalf("hung: did not complete within %s", timeout)
		return nil
	}
}

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
