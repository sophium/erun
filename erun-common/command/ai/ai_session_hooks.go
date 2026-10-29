package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AISessionHookPayload is the part of a hook payload the model reads; the
// event kind comes from the settings entry, not the payload.
type AISessionHookPayload struct {
	SessionID string `json:"session_id"`
}

// AISessionHookSessionID returns the payload's session id, refusing rather than
// guessing, since the id is the record's key.
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
