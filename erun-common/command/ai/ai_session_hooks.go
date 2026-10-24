package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
