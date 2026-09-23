package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// isolateAISessionCache points the activity cache at a throwaway directory, so
// a test reads back the record its own run wrote rather than whatever the
// machine running it has accumulated.
func isolateAISessionCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

// activityCommandTree builds the real command tree the settings address, so a
// test that resolves a path finds the verb the binary actually registers
// rather than one a test constructed beside it.
func activityCommandTree() *cobra.Command {
	root := &cobra.Command{Use: "erun"}
	root.AddCommand(newActivityCmd(nil, nil))
	return root
}

// TestAISessionHookVerbIsWhatTheInstalledSettingsAddress is the half of the
// wiring that lives in the command tree: the pod's Claude settings install a
// literal command string, and if that path does not resolve to a real verb with
// these flags, every hook fails on every turn boundary and the model resolves
// nothing -- the exact inertness this work exists to end.
func TestAISessionHookVerbIsWhatTheInstalledSettingsAddress(t *testing.T) {
	root := activityCommandTree()
	// Find takes the args after the root's own name, so the leading "erun" is
	// the one part of the installed path the tree does not resolve.
	verb, _, err := root.Find(strings.Fields(common.AISessionHookVerbPath)[1:])
	if err != nil {
		t.Fatalf("resolve %q in the command tree: %v", common.AISessionHookVerbPath, err)
	}
	if got := verb.CommandPath(); got != common.AISessionHookVerbPath {
		t.Fatalf("the installed hook command addresses %q, which resolves to %q", common.AISessionHookVerbPath, got)
	}
	for _, flag := range []string{"event", "tool", "tenant", "environment"} {
		if verb.Flags().Lookup(flag) == nil {
			t.Fatalf("the hook verb has no --%s flag, so the installed command cannot pass it", flag)
		}
	}
}

// TestAISessionHookCommandReportsTheStateTheModelClaims runs the installed
// command for every binding the settings carry, with the payload that tool
// event actually sends, and reads the state back out of the model. This is the
// writer the status model was missing: before it, `ResolveAISessionStatus`
// always had nothing to resolve and `awaiting-input` -- the state the whole
// model exists to surface -- could not be reached by anything.
//
// The environment is taken from the ambient ERUN_* pair rather than from flags,
// because that is how an installed hook runs: one settings file is shared by
// every environment, so nothing about one can be baked into the command.
func TestAISessionHookCommandReportsTheStateTheModelClaims(t *testing.T) {
	isolateAISessionCache(t)
	t.Setenv("ERUN_TENANT", "team")
	t.Setenv("ERUN_ENVIRONMENT", "dev")

	const wantAwaitingInput = common.AISessionStateAwaitingInput
	wantState := map[common.AISessionEventKind]common.AISessionState{
		common.AISessionEventTurnStart: common.AISessionStateBusy,
		common.AISessionEventToolUse:   common.AISessionStateBusy,
		common.AISessionEventTurnEnd:   wantAwaitingInput,
		common.AISessionEventNotify:    wantAwaitingInput,
		common.AISessionEventExit:      common.AISessionStateExited,
	}

	for i, binding := range common.AISessionHookBindings() {
		sessionID := fmt.Sprintf("0d5b1e0a-6f2b-4a0e-9a1f-2b1f8d3c4e%02d", i)
		t.Run(binding.ToolEvent, func(t *testing.T) {
			err := runAISessionHook(t, sessionID, binding.ModelEvent)
			if err != nil {
				t.Fatalf("hook %s: %v", binding.ToolEvent, err)
			}
			status, err := common.LoadAISessionStatus("team", "dev", sessionID)
			if err != nil {
				t.Fatalf("load status: %v", err)
			}
			if status.State != wantState[binding.ModelEvent] {
				t.Fatalf(
					"a %s hook reported %s, which reads as %s; want %s",
					binding.ToolEvent, binding.ModelEvent, status.State, wantState[binding.ModelEvent],
				)
			}
			if status.Tool != "claude" {
				t.Fatalf("the record lost the reporting tool: got %q", status.Tool)
			}
		})
	}
}

// TestAISessionHookRefusesWhatItCannotAttribute covers the three ways an
// invocation is not something to record, each of which must say why rather than
// filing the event against an environment or a session that did not send it.
func TestAISessionHookRefusesWhatItCannotAttribute(t *testing.T) {
	isolateAISessionCache(t)
	t.Setenv("ERUN_TENANT", "team")
	t.Setenv("ERUN_ENVIRONMENT", "dev")

	cases := []struct {
		name    string
		args    []string
		payload string
		want    string
	}{
		{
			name:    "unknown event",
			args:    []string{"hook", "--event", "turn-midway", "--tool", "claude"},
			payload: `{"session_id":"s1"}`,
			want:    "turn-midway",
		},
		{
			name:    "no session id in the payload",
			args:    []string{"hook", "--event", "turn-end", "--tool", "claude"},
			payload: `{"hook_event_name":"Stop"}`,
			want:    "session_id",
		},
		{
			name:    "payload that is not JSON",
			args:    []string{"hook", "--event", "turn-end", "--tool", "claude"},
			payload: "session_id=s1",
			want:    "not JSON",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := executeAISessionHook(t, tc.args, tc.payload)
			if err == nil {
				t.Fatalf("expected a refusal naming %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err.Error(), tc.want)
			}
		})
	}
}

// TestAISessionHookWithoutAnEnvironmentNamesTheNextAction pins the refusal a
// hook cannot make silently: one that resolves no environment would otherwise
// write the event against a guess. It names both the flags and the variables,
// because the two callers -- a settings entry in a pod, and an operator at a
// prompt -- reach it differently.
func TestAISessionHookWithoutAnEnvironmentNamesTheNextAction(t *testing.T) {
	isolateAISessionCache(t)
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")

	err := executeAISessionHook(t, []string{"hook", "--event", "turn-end", "--tool", "claude"}, `{"session_id":"s1"}`)
	if err == nil {
		t.Fatal("expected a refusal when no environment can be resolved")
	}
	for _, want := range []string{"tenant and environment", "--tenant and --environment", "ERUN_TENANT and ERUN_ENVIRONMENT"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err.Error(), want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "erun")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused hook must not have written anything, got stat err=%v", statErr)
	}
}

// runAISessionHook invokes the verb with the payload the given tool event sends,
// asserting it reports no failure.
func runAISessionHook(t *testing.T, sessionID string, modelEvent common.AISessionEventKind) error {
	t.Helper()
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/home/erun/.claude/projects/-home-erun-git-erun/%s.jsonl","cwd":"/home/erun/git/erun","hook_event_name":"%s"}`, sessionID, sessionID, modelEvent)
	err := executeAISessionHook(t, []string{"hook", "--event", string(modelEvent), "--tool", "claude"}, payload)
	if err != nil {
		return fmt.Errorf("run hook: %w", err)
	}
	return nil
}

// executeAISessionHook runs the hook verb off the same command tree the binary
// registers, with the payload arriving on stdin exactly as a tool delivers it.
func executeAISessionHook(t *testing.T, args []string, payload string) error {
	t.Helper()
	cmd := newActivityAISessionCmd()
	var out bytes.Buffer
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd.Execute()
}
