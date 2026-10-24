package build

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// heavyDualStreamScript writes over 64KB to both streams, so a draining bug hangs.
const heavyDualStreamScript = `for i in $(seq 1 20000); do
  echo "out-$i padding padding padding padding padding padding padding padding"
  echo "err-$i padding padding padding padding padding padding padding padding" 1>&2
done`

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

func TestRunDockerBuildOnceDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)
	t.Setenv("ERUN_DOCKER_BIN", stub)

	for _, verbosity := range []int{core.VerbosityInfo, core.VerbosityDebug} {
		var stdout, stderr strings.Builder
		err := runWithHangGuard(t, 20*time.Second, func() error {
			_, err := runDockerBuildOnce([]string{"build"}, ".", "tag", false, verbosity, &stdout, &stderr)
			return err
		})
		if err != nil {
			t.Fatalf("verbosity %d: runDockerBuildOnce: %v", verbosity, err)
		}
		if verbosity >= core.VerbosityDebug {
			if !strings.Contains(stdout.String(), "out-1 ") {
				t.Errorf("verbosity %d: stdout missing its own content", verbosity)
			}
			if !strings.Contains(stderr.String(), "err-1 ") {
				t.Errorf("verbosity %d: stderr missing its own content", verbosity)
			}
		}
	}
}

func TestRunDockerPushOnceDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)
	t.Setenv("ERUN_DOCKER_BIN", stub)

	var stdout, stderr strings.Builder
	err := runWithHangGuard(t, 20*time.Second, func() error {
		return shared.RunDockerPushOnce("tag", core.VerbosityDebug, &stdout, &stderr)
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

// writeExecutableScript writes body as an executable shell script and returns its path.
func writeExecutableScript(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/stub.sh"
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}
