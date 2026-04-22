package cmd

import (
	"bytes"
	"testing"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func TestTraceLoggerVerbosity(t *testing.T) {
	if got := common.TraceLoggerVerbosity(0); got != 0 {
		t.Fatalf("expected no logger verbosity, got %d", got)
	}
	if got := common.TraceLoggerVerbosity(1); got != 2 {
		t.Fatalf("expected -v to enable trace logging, got %d", got)
	}
	if got := common.TraceLoggerVerbosity(2); got != 3 {
		t.Fatalf("expected higher verbosity to keep increasing logger level, got %d", got)
	}
}

func TestCommandContextEnablesTraceDuringDryRunWithoutVerboseFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	addDryRunFlag(cmd)
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatalf("set dry-run: %v", err)
	}

	ctx := commandContext(cmd)
	ctx.TraceCommand("", "docker", "build", "-t", "example/image:1.0.0", ".")

	if got := stderr.String(); got == "" {
		t.Fatal("expected dry-run trace output without verbose flag")
	}
}

func TestRootCommandPrintsElapsedTimeOnError(t *testing.T) {
	cmd := newRootCommand(func(_ *cobra.Command, _ []string) error {
		return bytes.ErrTooLarge
	})
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--time"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected command error")
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("elapsed:")) {
		t.Fatalf("expected elapsed time output on stderr, got %q", got)
	}
}
