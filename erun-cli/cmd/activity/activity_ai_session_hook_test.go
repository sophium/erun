package activity

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/activity/aisession"
	"github.com/spf13/cobra"
)

func isolateAISessionCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func activityCommandTree() *cobra.Command {
	root := &cobra.Command{Use: "erun"}
	root.AddCommand(NewActivityCmd(nil, nil))
	return root
}

// TestAISessionHookVerbIsWhatTheInstalledSettingsAddress: an unresolvable installed path would fail
// every hook on every turn boundary.
func TestAISessionHookVerbIsWhatTheInstalledSettingsAddress(t *testing.T) {
	root := activityCommandTree()
	// Find takes args after the root's own name.
	verb, _, err := root.Find(strings.Fields(podruntime.AISessionHookVerbPath)[1:])
	if err != nil {
		t.Fatalf("resolve %q in the command tree: %v", podruntime.AISessionHookVerbPath, err)
	}
	if got := verb.CommandPath(); got != podruntime.AISessionHookVerbPath {
		t.Fatalf("the installed hook command addresses %q, which resolves to %q", podruntime.AISessionHookVerbPath, got)
	}
	for _, flag := range []string{"event", "tool", "tenant", "environment"} {
		if verb.Flags().Lookup(flag) == nil {
			t.Fatalf("the hook verb has no --%s flag, so the installed command cannot pass it", flag)
		}
	}
}

// TestAISessionHookCommandReportsTheStateTheModelClaims: the environment comes from ambient ERUN_*
// vars, as an installed hook runs.
func TestAISessionHookCommandReportsTheStateTheModelClaims(t *testing.T) {
	isolateAISessionCache(t)
	t.Setenv("ERUN_TENANT", "team")
	t.Setenv("ERUN_ENVIRONMENT", "dev")

	const wantAwaitingInput = model.AISessionStateAwaitingInput
	wantState := map[model.AISessionEventKind]model.AISessionState{
		model.AISessionEventTurnStart: model.AISessionStateBusy,
		model.AISessionEventToolUse:   model.AISessionStateBusy,
		model.AISessionEventTurnEnd:   wantAwaitingInput,
		model.AISessionEventNotify:    wantAwaitingInput,
		model.AISessionEventExit:      model.AISessionStateExited,
	}

	for i, binding := range podruntime.AISessionHookBindings() {
		sessionID := fmt.Sprintf("0d5b1e0a-6f2b-4a0e-9a1f-2b1f8d3c4e%02d", i)
		t.Run(binding.ToolEvent, func(t *testing.T) {
			err := runAISessionHook(t, sessionID, binding.ModelEvent)
			if err != nil {
				t.Fatalf("hook %s: %v", binding.ToolEvent, err)
			}
			status, err := aisession.LoadAISessionStatus("team", "dev", sessionID)
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

func runAISessionHook(t *testing.T, sessionID string, modelEvent model.AISessionEventKind) error {
	t.Helper()
	payload := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/home/erun/.claude/projects/-home-erun-git-erun/%s.jsonl","cwd":"/home/erun/git/erun","hook_event_name":"%s"}`, sessionID, sessionID, modelEvent)
	err := executeAISessionHook(t, []string{"hook", "--event", string(modelEvent), "--tool", "claude"}, payload)
	if err != nil {
		return fmt.Errorf("run hook: %w", err)
	}
	return nil
}

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
