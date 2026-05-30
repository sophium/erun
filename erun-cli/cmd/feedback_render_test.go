package cmd

import (
	"testing"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func TestCommandVerbosityNoShellSilencesByDefault(t *testing.T) {
	// --no-shell is the eval-friendly output mode for `erun open`; when
	// the user has not opted into -v / -vv or --dry-run, the command must
	// keep stderr silent so the wrapping alias doesn't leak audit and
	// trace lines into the user's terminal.
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "no flags", args: nil, want: common.VerbosityInfo},
		{name: "no-shell alone is silent", args: []string{"--no-shell"}, want: common.VerbosityInfo - 1},
		{name: "no-shell + -v keeps debug verbosity", args: []string{"--no-shell", "-v"}, want: common.VerbosityDebug},
		{name: "no-shell + -vv keeps trace verbosity", args: []string{"--no-shell", "-vv"}, want: common.VerbosityTrace},
		{name: "no-shell + --dry-run keeps trace verbosity", args: []string{"--no-shell", "--dry-run"}, want: common.VerbosityTrace},
		{name: "--dry-run alone keeps trace verbosity", args: []string{"--dry-run"}, want: common.VerbosityTrace},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			leaf := newOpenLikeCommandForTesting()
			if err := leaf.ParseFlags(tc.args); err != nil {
				t.Fatalf("parse flags %v: %v", tc.args, err)
			}
			got := commandVerbosity(leaf)
			if got != tc.want {
				t.Fatalf("commandVerbosity %v = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestIsNoShellCommand(t *testing.T) {
	leaf := newOpenLikeCommandForTesting()
	if isNoShellCommand(leaf) {
		t.Fatalf("fresh open command should not report --no-shell")
	}
	if err := leaf.Flags().Set("no-shell", "true"); err != nil {
		t.Fatalf("set no-shell: %v", err)
	}
	if !isNoShellCommand(leaf) {
		t.Fatalf("expected isNoShellCommand to be true after setting --no-shell")
	}
}

func newOpenLikeCommandForTesting() *cobra.Command {
	// Mirrors the shape of the real open command tree for the verbosity
	// path: root owns the persistent --verbose, leaf owns --dry-run and
	// --no-shell. commandVerbosity reads flags off the leaf via cobra's
	// flag inheritance.
	root := &cobra.Command{Use: "erun"}
	root.PersistentFlags().CountP("verbose", "v", "")
	addDryRunFlag(root)
	leaf := &cobra.Command{Use: "open", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	addDryRunFlag(leaf)
	leaf.Flags().Bool("no-shell", false, "")
	root.AddCommand(leaf)
	return leaf
}
