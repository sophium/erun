package eruncommon

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The entrypoint's shell/JS twin of AISessionHookBindings. The runtime image
// installs the hooks in the pod's Claude settings, where Go cannot reach: the
// settings file is written by the image's own boot script, in node. Two
// implementations of one mapping is the coupling this test exists for -- the
// failure it prevents is silent and total, because a mapping that drifts
// installs a hook that reports the wrong state, or none at all, and the model
// simply resolves nothing rather than erroring.
const (
	entrypointAISessionHooksTable = `aiSessionHooks = [`
	aiSessionHookEntrypointRow    = `(?m)^\s*\['([A-Za-z]+)', '([a-z-]+)'\],$`
	aiSessionHookEntrypointVerb   = AISessionHookVerbPath + " --event ${modelEvent} --tool " + defaultAITool
)

// TestEntrypointInstallsTheAISessionTurnBoundaryHooks is the reproduction of
// the reported defect: the status model had readers and no writer, because no
// layer that composes the AI tool's settings installed a hook. The settings
// this test reads are the pod's -- the file every session `erun open --ai` and
// every in-pod agent actually runs under -- so a table here that is missing,
// empty, or disagrees with this package's own binding is the whole defect,
// caught before it ships instead of after a badge that never fires.
func TestEntrypointInstallsTheAISessionTurnBoundaryHooks(t *testing.T) {
	script := readEntrypointScript(t)
	rows := entrypointAISessionHookRows(t, script)
	want := AISessionHookBindings()

	if len(rows) == 0 {
		t.Fatalf(
			"%s installs no AI-session turn-boundary hooks: no '%s' table was found, so the pod's Claude settings report nothing and every reader of the AI-session status model resolves from events that never arrive",
			entrypointPath, entrypointAISessionHooksTable,
		)
	}
	if len(rows) != len(want) {
		t.Fatalf("entrypoint installs %d AI-session hook bindings, want %d: got %+v", len(rows), len(want), rows)
	}
	for i, binding := range want {
		if rows[i] != binding {
			t.Fatalf("entrypoint hook binding %d is %+v, want %+v", i, rows[i], binding)
		}
	}

	// The table is not the deliverable on its own: what the settings entry runs
	// has to reach the verb that writes the record, with the event and the tool
	// it recorded. A command template that names neither installs a hook that
	// fails on every turn boundary.
	if !strings.Contains(script, aiSessionHookEntrypointVerb) {
		t.Fatalf("the entrypoint's installed hook command does not name the report verb; want it to contain %q", aiSessionHookEntrypointVerb)
	}
}

// TestAISessionHookBindingsResolveToTheStateTheyClaim pins the mapping to the
// model rather than to prose: an installed hook's value is the state the
// resolver then reports for it, and the two that mean the human has control
// must resolve to AwaitingInput -- the state this whole model exists to
// surface.
func TestAISessionHookBindingsResolveToTheStateTheyClaim(t *testing.T) {
	wantState := map[AISessionEventKind]AISessionState{
		AISessionEventTurnStart: AISessionStateBusy,
		AISessionEventToolUse:   AISessionStateBusy,
		AISessionEventTurnEnd:   AISessionStateAwaitingInput,
		AISessionEventNotify:    AISessionStateAwaitingInput,
		AISessionEventExit:      AISessionStateExited,
	}
	reported := map[AISessionEventKind]bool{}
	for _, binding := range AISessionHookBindings() {
		state, ok := wantState[binding.ModelEvent]
		if !ok {
			t.Fatalf("binding %s reports %q, which no hook can produce a state for", binding.ToolEvent, binding.ModelEvent)
		}
		reported[binding.ModelEvent] = true
		got := ResolveAISessionStatus(AISessionRecord{SessionID: "s", Event: binding.ModelEvent})
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

func readEntrypointScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(entrypointPath)
	if err != nil {
		t.Fatalf("read %s: %v", entrypointPath, err)
	}
	return string(data)
}

// entrypointAISessionHookRows reads the entrypoint's binding table out of the
// script and evaluates it, rather than restating the mapping here: editing the
// shell table alone must fail this test too.
func entrypointAISessionHookRows(t *testing.T, script string) []AISessionHookBinding {
	t.Helper()
	start := strings.Index(script, entrypointAISessionHooksTable)
	if start < 0 {
		return nil
	}
	end := strings.Index(script[start:], "];")
	if end < 0 {
		t.Fatalf("%s: the '%s' table is not terminated", entrypointPath, entrypointAISessionHooksTable)
	}
	rows := regexp.MustCompile(aiSessionHookEntrypointRow).FindAllStringSubmatch(script[start:start+end], -1)
	bindings := make([]AISessionHookBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, AISessionHookBinding{ToolEvent: row[1], ModelEvent: AISessionEventKind(row[2])})
	}
	return bindings
}
