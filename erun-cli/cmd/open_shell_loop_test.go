package cmd

import (
	"bytes"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// TestRunShellLoopEndsCleanlyWhenSessionTakenOver pins the takeover handover
// from #478: when another ERun window re-attaches the persistent session,
// ExecShell surfaces ErrShellSessionTakenOver and the loop must end cleanly —
// no error, no relaunch — after printing the stable notice line the desktop
// matches to suppress its reconnect. runShellLoop is TTY-gated, so this stays
// a same-package unit test per the documented integration coverage gap.
func TestRunShellLoopEndsCleanlyWhenSessionTakenOver(t *testing.T) {
	var output bytes.Buffer
	runner := &resolvedOpenRunner{
		ctx: common.Context{Logger: common.NewLoggerWithWriters(0, &output, &output)},
		openShell: func(common.Context, common.ShellLaunchParams) error {
			return common.ErrShellSessionTakenOver
		},
	}

	if err := runner.runShellLoop(common.ShellLaunchParams{}); err != nil {
		t.Fatalf("takeover must end the shell loop cleanly, got %v", err)
	}
	if !strings.Contains(output.String(), common.ShellSessionTakenOverNotice) {
		t.Fatalf("expected the taken-over notice %q in output, got %q",
			common.ShellSessionTakenOverNotice, output.String())
	}
}
