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
	requireNoError(t, cmd.Flags().Set("dry-run", "true"), "set dry-run")

	ctx := commandContext(cmd)
	ctx.TraceCommand("", "docker", "build", "-t", "example/image:1.0.0", ".")

	if got := stderr.String(); got == "" {
		t.Fatal("expected dry-run trace output without verbose flag")
	}
}

func TestRootCommandAuditsOnlyWhenTraceEnabled(t *testing.T) {
	cmd := newRootCommand(func(_ *cobra.Command, _ []string) error {
		return nil
	})
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs(nil)

	requireNoError(t, cmd.Execute(), "Execute failed")
	if got := stderr.String(); got != "" {
		t.Fatalf("expected no audit without trace, got %q", got)
	}

	cmd = newRootCommand(func(_ *cobra.Command, _ []string) error {
		return nil
	})
	stderr = new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if got := stderr.String(); got != "audit: erun\n" {
		t.Fatalf("unexpected audit output: %q", got)
	}
}

func TestExecCommandEnablesTraceByDefault(t *testing.T) {
	root := newRootCommand(func(_ *cobra.Command, _ []string) error {
		return nil
	})
	execCmd := newCommandGroup("exec", "Repository execution utilities", &cobra.Command{
		Use: "noop",
		RunE: func(cmd *cobra.Command, _ []string) error {
			commandContext(cmd).Trace("exec trace")
			return nil
		},
	})
	addCommands(root, execCmd)
	stderr := new(bytes.Buffer)
	root.SetErr(stderr)
	root.SetArgs([]string{"exec", "noop"})

	requireNoError(t, root.Execute(), "Execute failed")
	got := stderr.String()
	if !bytes.Contains([]byte(got), []byte("audit: erun exec noop")) || !bytes.Contains([]byte(got), []byte("exec trace")) {
		t.Fatalf("expected exec trace by default, got %q", got)
	}
}

func TestAuditCommandRedactsSensitiveArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "raw"}
	parent := newCommandGroup("exec", "Repository execution utilities", cmd)
	root := newRootCommand(func(_ *cobra.Command, _ []string) error { return nil })
	addCommands(root, parent)

	got := formatAuditCommand(cmd, []string{"deploy", "--token", "secret-value", "--password=hidden", "ok"})
	want := "erun exec raw deploy --token <redacted> --password=<redacted> ok"
	if got != want {
		t.Fatalf("unexpected audit command: got %q want %q", got, want)
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
