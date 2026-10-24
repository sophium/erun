package cmd

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	activitypkg "github.com/sophium/erun/cmd/activity"
	"github.com/sophium/erun/cmd/kubecontext"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/spf13/cobra"
)

// Every command with a structured form must emit it for --output json, not just for its own --json alias.
// Commands whose --json form a golden already drives are covered by erun-integration.
func TestOutputJSONEmitsStructuredResult(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	leases := []model.EnvironmentActivityLease{{
		ID:        "build",
		Name:      "build",
		StartedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
	}}

	cases := []struct {
		name   string
		render func(core.Context) error
	}{
		{"activity lease list", func(ctx core.Context) error {
			return activitypkg.WriteActivityLeases(ctx, leases, now, false)
		}},
		{"activity sample", func(ctx core.Context) error {
			return activitypkg.WriteActivitySampleResult(ctx, model.ResidentActivityResult{Busy: true}, false)
		}},
		{"context list", func(ctx core.Context) error {
			return kubecontext.WriteCloudContextList(ctx, model.CloudContextListResult{CloudContexts: []model.CloudContextStatus{{Status: "running"}}})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := core.Context{Output: model.OutputJSON, Stdout: &buf, Stderr: &buf}
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

func walkCommandTree(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommandTree(child, fn)
	}
}

// commandTreePath returns a command's path below the root, matching command_tree.go's keys.
func commandTreePath(root, cmd *cobra.Command) string {
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), root.Name()))
}
