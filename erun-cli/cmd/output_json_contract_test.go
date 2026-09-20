package cmd

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// The documented global --output json promises "structured result on stdout for
// orchestrators". A command that answers it with its human text report succeeds
// and returns prose, so a consumer piping into jq cannot tell a wrong spelling
// from a right one. Every command with a structured form owes that form to
// --output json, whether or not it also carries its own --json alias.
func TestOutputJSONEmitsStructuredResult(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	leases := []common.EnvironmentActivityLease{{
		ID:        "build",
		Name:      "build",
		StartedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
	}}

	cases := []struct {
		name   string
		render func(common.Context) error
	}{
		{"idle", func(ctx common.Context) error {
			return writeIdleResult(ctx, common.EnvironmentIdleStatus{StopEligible: true, SecondsUntilStop: 300}, false)
		}},
		{"whip", func(ctx common.Context) error {
			return writeWhipReport(ctx, common.WhipReport{
				DryRun:  true,
				Results: []common.WhipResult{{Reason: "no candidate"}},
			}, false)
		}},
		{"activity lease list", func(ctx common.Context) error {
			return writeActivityLeases(ctx, leases, now, false)
		}},
		{"activity sample", func(ctx common.Context) error {
			return writeActivitySampleResult(ctx, common.ResidentActivityResult{Busy: true}, false)
		}},
		{"activity ai-session status", func(ctx common.Context) error {
			return writeAISessionStatuses(commandCarryingOutput(ctx), []common.AISessionStatus{{SessionID: "s1"}}, false)
		}},
		{"list", func(ctx common.Context) error {
			return writeListResult(ctx, common.ListResult{ConfigDirectory: "/tmp/erun"})
		}},
		{"context list", func(ctx common.Context) error {
			return writeCloudContextList(ctx, []common.CloudContextStatus{{Status: "running"}})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := common.Context{Output: common.OutputJSON, Stdout: &buf, Stderr: &buf}
			if err := tc.render(ctx); err != nil {
				t.Fatalf("render: %v", err)
			}
			out := bytes.TrimSpace(buf.Bytes())
			if len(out) == 0 {
				t.Fatal("--output json produced no output at all")
			}
			if !json.Valid(out) {
				t.Fatalf("--output json produced non-JSON output: %q", out)
			}
		})
	}
}

// Text mode is the default and must stay prose: the assertion above passes
// trivially for a command that always emits JSON, which would trade one silent
// substitution for another.
func TestTextOutputStaysProse(t *testing.T) {
	var buf bytes.Buffer
	ctx := common.Context{Output: common.OutputText, Stdout: &buf, Stderr: &buf}
	if err := writeIdleResult(ctx, common.EnvironmentIdleStatus{SecondsUntilStop: 300}, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatalf("default text mode emitted JSON: %q", buf.String())
	}
}

// A command growing its own --json flag is how idle came to answer the
// documented global flag with prose while the structured document it wanted
// existed behind a different switch. Declaring each alias keeps that from
// recurring silently, and asking whether --output json reaches it is the
// reviewable question at the moment the flag is added.
func TestEveryJSONFlagIsADeclaredOutputAlias(t *testing.T) {
	root := CommandTreeForAudit()
	var undeclared []string
	walkCommandTree(root, func(cmd *cobra.Command) {
		if cmd.Flags().Lookup("json") == nil {
			return
		}
		path := commandTreePath(root, cmd)
		if !IsOutputJSONAliasCommand(path) {
			undeclared = append(undeclared, path)
		}
	})
	if len(undeclared) == 0 {
		return
	}
	sort.Strings(undeclared)
	t.Fatalf("commands carry a --json flag without declaring it as an alias of the global --output json: %v\n"+
		"Add each to erun-cli/cmd/command_tree.go's outputJSONAliasCommands after making --output json reach it.",
		undeclared)
}

// commandCarryingOutput adapts the render seams that read a Context out of the
// command's own flags, so they can be exercised from the same table.
func commandCarryingOutput(ctx common.Context) *cobra.Command {
	cmd := &cobra.Command{Use: "status"}
	cmd.Flags().String("output", string(ctx.Output), "")
	cmd.SetOut(ctx.Stdout)
	cmd.SetErr(ctx.Stderr)
	return cmd
}

func walkCommandTree(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommandTree(child, fn)
	}
}

// commandTreePath returns a command's path below the root, matching the keys in
// command_tree.go's declaration maps (e.g. "activity lease list").
func commandTreePath(root, cmd *cobra.Command) string {
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), root.Name()))
}
