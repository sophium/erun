package ai

import (
	"testing"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/model"
)

// TestAISessionHookBindingsResolveToTheStateTheyClaim pins the mapping to the
// model rather than to prose: an installed hook's value is the state the
// resolver then reports for it, and the two that mean the human has control
// must resolve to AwaitingInput -- the state this whole model exists to
// surface.
func TestAISessionHookBindingsResolveToTheStateTheyClaim(t *testing.T) {
	wantState := map[model.AISessionEventKind]model.AISessionState{
		model.AISessionEventTurnStart: model.AISessionStateBusy,
		model.AISessionEventToolUse:   model.AISessionStateBusy,
		model.AISessionEventTurnEnd:   model.AISessionStateAwaitingInput,
		model.AISessionEventNotify:    model.AISessionStateAwaitingInput,
		model.AISessionEventExit:      model.AISessionStateExited,
	}
	reported := map[model.AISessionEventKind]bool{}
	for _, binding := range eruncommon.AISessionHookBindings() {
		state, ok := wantState[binding.ModelEvent]
		if !ok {
			t.Fatalf("binding %s reports %q, which no hook can produce a state for", binding.ToolEvent, binding.ModelEvent)
		}
		reported[binding.ModelEvent] = true
		got := model.ResolveAISessionStatus(model.AISessionRecord{SessionID: "s", Event: binding.ModelEvent})
		if got.State != state {
			t.Fatalf("binding %s reports %q, which resolves to %s, want %s", binding.ToolEvent, binding.ModelEvent, got.State, state)
		}
	}
	for kind := range wantState {
		if !reported[kind] {
			t.Fatalf("no hook reports %q, so a session in that state can never be surfaced", kind)
		}
	}
}

// TestAISessionHookPayloadReadsTheSessionsOwnConversationID covers the payload
// a hook is actually handed, including the fields the model ignores: Claude
// Code sends the whole event context, and only session_id is the key the
// record is filed under.
func TestAISessionHookPayloadReadsTheSessionsOwnConversationID(t *testing.T) {
	payload := []byte(`{
	  "session_id": "0d5b1e0a-6f2b-4a0e-9a1f-2b1f8d3c4e77",
	  "transcript_path": "/home/erun/.claude/projects/-home-erun-git-erun/0d5b1e0a.jsonl",
	  "cwd": "/home/erun/git/erun",
	  "hook_event_name": "Stop",
	  "stop_hook_active": false
	}`)
	sessionID, err := AISessionHookSessionID(payload)
	if err != nil {
		t.Fatalf("read the hook payload: %v", err)
	}
	if sessionID != "0d5b1e0a-6f2b-4a0e-9a1f-2b1f8d3c4e77" {
		t.Fatalf("session id: got %q", sessionID)
	}

	for label, body := range map[string]string{
		"empty":         "  \n",
		"not JSON":      "session_id=xyz",
		"no session id": `{"hook_event_name":"Stop"}`,
		"blank id":      `{"session_id":"   "}`,
	} {
		t.Run("refuses "+label, func(t *testing.T) {
			if _, err := AISessionHookSessionID([]byte(body)); err == nil {
				t.Fatalf("expected %q to be refused; a guessed session id records the event against a session that did not send it", body)
			}
		})
	}
}
