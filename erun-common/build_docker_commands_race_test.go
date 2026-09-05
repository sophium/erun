package eruncommon

import (
	"os"
	"strings"
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

// TestRunDockerBuildOnceDrainsHeavyOutputConcurrently reproduces the build
// deadlock directly against runDockerBuildOnce, using a stub "docker"
// (ERUN_DOCKER_BIN) that writes far more than one pipe buffer's worth on both
// stdout and stderr. Before the fix, the VerbosityDebug branch wired a single
// shared bytes.Buffer into two independently-built io.MultiWriters (one per
// stream), which os/exec's same-writer dedup does not recognize as identical
// -- so two copier goroutines wrote the same *bytes.Buffer with no
// synchronization. Runs at both the default and VerbosityDebug branch since
// the two used to build the wiring differently.
func TestRunDockerBuildOnceDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)
	t.Setenv("ERUN_DOCKER_BIN", stub)

	for _, verbosity := range []int{VerbosityInfo, VerbosityDebug} {
		var stdout, stderr strings.Builder
		err := runWithHangGuard(t, 20*time.Second, func() error {
			return runDockerBuildOnce([]string{"build"}, ".", "tag", false, verbosity, &stdout, &stderr)
		})
		if err != nil {
			t.Fatalf("verbosity %d: runDockerBuildOnce: %v", verbosity, err)
		}
		if verbosity >= VerbosityDebug {
			if !strings.Contains(stdout.String(), "out-1 ") {
				t.Errorf("verbosity %d: stdout missing its own content", verbosity)
			}
			if !strings.Contains(stderr.String(), "err-1 ") {
				t.Errorf("verbosity %d: stderr missing its own content", verbosity)
			}
		}
	}
}

// TestRunDockerPushOnceDrainsHeavyOutputConcurrently is the same
// reproduction against runDockerPushOnce, which -- unlike
// runDockerBuildOnce -- built the racy double-MultiWriter wiring
// unconditionally rather than only at VerbosityDebug.
func TestRunDockerPushOnceDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)
	t.Setenv("ERUN_DOCKER_BIN", stub)

	var stdout, stderr strings.Builder
	err := runWithHangGuard(t, 20*time.Second, func() error {
		return runDockerPushOnce("tag", VerbosityDebug, &stdout, &stderr)
	})
	if err != nil {
		t.Fatalf("runDockerPushOnce: %v", err)
	}
	if !strings.Contains(stdout.String(), "out-1 ") {
		t.Error("stdout missing its own content")
	}
	if !strings.Contains(stderr.String(), "err-1 ") {
		t.Error("stderr missing its own content")
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
