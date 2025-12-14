package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	cmdpkg "github.com/sophium/erun/cmd"
)

func TestRootCommandDisplaysHelpWhenNoArgs(t *testing.T) {
	cmd := cmdpkg.NewRootCmd()
	t.Setenv("HOME", t.TempDir())
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root command returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "erun is a skeleton CLI built with Cobra.") {
		t.Fatalf("help text missing, got: %q", output)
	}
}
