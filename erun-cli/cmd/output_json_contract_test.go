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
			return writeCloudContextList(ctx, common.CloudContextListResult{CloudContexts: []common.CloudContextStatus{{Status: "running"}}})
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

// TestEmptyListingCommandsEmitAnArrayNotNull pins every command whose whole
// structured result is a slice it builds by appending. These reached
// --output json as the literal document `null` when the list was empty, while
// `exec job status` answered `[]` for the same condition and `review list`
// answered a JSON array whenever anything matched -- so one command's result
// changed type with cardinality, and a caller had no way to tell which shape
// it would get.
//
// The nil slices below are what those handlers hand to WriteResult unchanged,
// which is exactly the value the reproduction measured.
func TestEmptyListingCommandsEmitAnArrayNotNull(t *testing.T) {
	cases := []struct {
		command string
		value   any
	}{
		{"gate list", []common.PlatformGateRun(nil)},
		{"jobs list", []common.PlatformJob(nil)},
		{"review list", []common.PlatformReview(nil)},
		{"review queue list", []common.PlatformReview(nil)},
		{"review reviewers", []common.PlatformReviewer(nil)},
		{"platform tenant list", []common.PlatformTenant(nil)},
		{"platform user list", []common.PlatformUser(nil)},
		{"platform environment list", []common.PlatformEnvironment(nil)},
		{"platform context list", []common.PlatformContext(nil)},
		{"build profile", []common.TimingRecordSummary(nil)},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := common.Context{Output: common.OutputJSON, Stdout: &buf, Stderr: &buf}
			if err := ctx.WriteResult(tc.value); err != nil {
				t.Fatalf("write result: %v", err)
			}

			out := bytes.TrimSpace(buf.Bytes())
			if bytes.Equal(out, []byte("null")) {
				t.Fatalf("%s answered an empty listing with the bare document null; a caller "+
					"cannot tell it from a result that was never determined", tc.command)
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("%s did not emit a JSON array for an empty listing: %v (%s)", tc.command, err, out)
			}
			if rows == nil {
				t.Fatalf("%s emitted a document that is not []: %s", tc.command, out)
			}
		})
	}
}

// TestReviewListKeepsOneTypeAcrossCardinalities is the axis the report called
// the sharper form of the defect: not two tools disagreeing, but one command
// disagreeing with itself. A populated list always serialised as an array, so
// a caller that developed against it and iterated the result worked until the
// list came back empty. Both are arrays now.
func TestReviewListKeepsOneTypeAcrossCardinalities(t *testing.T) {
	render := func(t *testing.T, reviews []common.PlatformReview) []json.RawMessage {
		t.Helper()
		var buf bytes.Buffer
		ctx := common.Context{Output: common.OutputJSON, Stdout: &buf, Stderr: &buf}
		if err := ctx.WriteResult(reviews); err != nil {
			t.Fatalf("write result: %v", err)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rows); err != nil {
			t.Fatalf("review list is not a JSON array: %v (%s)", err, buf.String())
		}
		if rows == nil {
			t.Fatalf("review list serialised as null rather than []: %s", buf.String())
		}
		return rows
	}

	if got := len(render(t, nil)); got != 0 {
		t.Fatalf("expected an empty array, got %d rows", got)
	}
	if got := len(render(t, []common.PlatformReview{{ReviewID: "r1"}})); got != 1 {
		t.Fatalf("expected one row, got %d", got)
	}
}

// emptyCloudConfig is the state the reported defect is about: a config that
// resolves fine and lists no cloud contexts.
type emptyCloudConfig struct{}

func (emptyCloudConfig) LoadERunConfig() (common.ERunConfig, string, error) {
	return common.ERunConfig{}, "", nil
}

func (emptyCloudConfig) SaveERunConfig(common.ERunConfig) error { return nil }

// TestContextListJSONAlwaysCarriesTheCollection drives the real command body
// over the CLI half of the shape context_list shares with the MCP tool: an
// environment with no managed contexts answered `{}`, so the field a caller
// reads was not merely null but gone, indistinguishable from a result that
// does not report contexts at all.
func TestContextListJSONAlwaysCarriesTheCollection(t *testing.T) {
	var buf bytes.Buffer
	ctx := common.Context{Output: common.OutputJSON, Stdout: &buf, Stderr: &buf}
	if err := runContextListCommand(ctx, emptyCloudConfig{}, common.CloudContextDependencies{}); err != nil {
		t.Fatalf("run context list: %v", err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &document); err != nil {
		t.Fatalf("context list is not a JSON object: %v (%s)", err, buf.String())
	}
	raw, present := document["cloudContexts"]
	if !present {
		t.Fatalf("context list dropped the collection field entirely: %s", buf.String())
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("cloudContexts is not an array: %v (%s)", err, raw)
	}
	if rows == nil {
		t.Fatalf("cloudContexts is null rather than []: %s", buf.String())
	}
}
