package eruncommon

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestToolCaptureDrainsHeavyOutputConcurrently exercises Context.ToolCapture
// the same way build_docker_commands_race_test.go exercises the docker build
// wiring: at VerbosityDebug, ToolCapture used to build cmd.Stdout and
// cmd.Stderr from two independently-built teeWriter/io.MultiWriter calls that
// both wrote into the same shared *bytes.Buffer, so os/exec's two per-stream
// copier goroutines wrote it with no synchronization -- the same shape
// deploy.go's helmOutputCapture exists to avoid for helm deploy. ToolCapture
// backs cluster_registry_kubectl.go, doctor_remote_init.go, and terraform.go.
func TestToolCaptureDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)

	for _, verbosity := range []int{VerbosityInfo, VerbosityDebug} {
		// Stdout/Stderr must be non-nil at VerbosityDebug: teeWriter shortcuts
		// to returning the bare capture buffer unwrapped when its primary
		// writer is nil, which would collapse stdout/stderr onto the exact
		// same object (safe) and mask the very bug this test exists to catch.
		ctx := Context{Verbosity: verbosity, Stdout: io.Discard, Stderr: io.Discard}
		capture := ctx.ToolCapture()
		cmd := Command(stub)
		cmd.Stdout = capture.Stdout()
		cmd.Stderr = capture.Stderr()

		err := runWithHangGuard(t, 20*time.Second, cmd.Run)
		if err != nil {
			t.Fatalf("verbosity %d: cmd.Run: %v", verbosity, err)
		}
		if !strings.Contains(capture.Output(), "out-1 ") {
			t.Errorf("verbosity %d: output missing stdout content", verbosity)
		}
		if !strings.Contains(capture.Output(), "err-1 ") {
			t.Errorf("verbosity %d: output missing stderr content", verbosity)
		}
	}
}
