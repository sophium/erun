package build

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// Both streams write one shared capture buffer from separate copier goroutines.
func TestToolCaptureDrainsHeavyOutputConcurrently(t *testing.T) {
	stub := writeExecutableScript(t, heavyDualStreamScript)

	for _, verbosity := range []int{core.VerbosityInfo, core.VerbosityDebug} {
		// Non-nil writers keep teeWriter from collapsing both streams onto one object and masking the race.
		ctx := core.Context{Verbosity: verbosity, Stdout: io.Discard, Stderr: io.Discard}
		capture := ctx.ToolCapture()
		cmd := core.Command(stub)
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
