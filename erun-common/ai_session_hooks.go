package eruncommon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The AI tools' own hook events are the producer the structured AI-session
// status model needs. AISessionEventKind names a turn-boundary signal; these
// types name the tool-side event each one is reported under, and the payload a
// hook invocation hands the report verb. Without an installed hook the model
// resolves from events nothing ever reports, so AwaitingInput -- the state this
// model exists to surface, and the one a PTY output-volume heuristic cannot
// represent -- is unreachable no matter how many readers it has.

// AISessionHookVerbPath is the CLI verb an installed hook runs. It is not the
// only way to reach the write side -- `report` addresses a session a caller
// already knows -- but it is the one a settings file can install, because a
// hook is handed its session id by the tool on stdin rather than as an argv
// token, and because the environment it reports against is the one the session
// is already running in.
const AISessionHookVerbPath = "erun activity ai-session hook"

// AISessionHookBinding binds one of the AI tool's own hook events to the model
// event a report installed on it records.
type AISessionHookBinding struct {
	// ToolEvent is the hook event name in the tool's own settings vocabulary.
	ToolEvent string
	// ModelEvent is the turn-boundary event a report bound to ToolEvent writes.
	ModelEvent AISessionEventKind
}

// AISessionHookBindings is the whole turn-boundary mapping, and is the
// definition the pod's settings composer mirrors. The split between Busy and
// AwaitingInput is the reason it exists:
//
//   - Busy is reported at the turn's start AND on every tool call. a report
//     written only at the start of a turn is stale for every minute of that
//     turn after it, so a turn longer than any staleness bound would read as
//     idle while it is still working.
//   - AwaitingInput is reported by the events that mean control went back to
//     the human: the turn's end, and a notification raised mid-turn when the
//     tool blocks on a permission or a question. These are direct statements
//     about the tool's state, not inferences from silence, which is what lets
//     a session that has been quiet for an hour stay distinguishable from one
//     that ended.
//   - Exited is reported once, when the session ends.
//
// Two events are deliberately absent. SessionStart would have to report Busy
// for a session nobody has prompted yet, which is the opposite of what it is
// doing; and a subagent's own stop is not the session's turn boundary, so
// binding it to TurnEnd would report AwaitingInput for a session that is still
// mid-turn working through the subagent's result.
func AISessionHookBindings() []AISessionHookBinding {
	return []AISessionHookBinding{
		{ToolEvent: "UserPromptSubmit", ModelEvent: AISessionEventTurnStart},
		{ToolEvent: "PreToolUse", ModelEvent: AISessionEventToolUse},
		{ToolEvent: "PostToolUse", ModelEvent: AISessionEventToolUse},
		{ToolEvent: "Stop", ModelEvent: AISessionEventTurnEnd},
		{ToolEvent: "Notification", ModelEvent: AISessionEventNotify},
		{ToolEvent: "SessionEnd", ModelEvent: AISessionEventExit},
	}
}

// AISessionHookPayload is the part of an AI tool's hook payload the model
// reads. A hook carries the conversation the session is actually on as
// `session_id`, on every event; nothing else is needed, because which model
// event to record is a property of the settings entry the command is bound to
// rather than of the payload.
type AISessionHookPayload struct {
	SessionID string `json:"session_id"`
}

// AISessionHookSessionID extracts the session id a hook invocation reports.
// A payload that is not JSON, or that carries no session id, is refused rather
// than defaulted: the session id is the key the record is filed under, so a
// guessed one would attribute the event to a session that did not send it.
func AISessionHookSessionID(payload []byte) (string, error) {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "", fmt.Errorf("the hook payload on stdin is empty")
	}
	var parsed AISessionHookPayload
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", fmt.Errorf("the hook payload on stdin is not JSON: %w", err)
	}
	sessionID := strings.TrimSpace(parsed.SessionID)
	if sessionID == "" {
		return "", fmt.Errorf("the hook payload on stdin carries no session_id")
	}
	return sessionID, nil
}
