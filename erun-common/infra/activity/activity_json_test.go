package activity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// TestAISessionJSONFollowsTheSameNeverSetRule covers the sibling surface that
// carried the identical tag: an AI session that has recorded nothing is not a
// session whose last activity was the zero instant either. It is asserted
// separately because `erun activity ai-session` and the MCP `ai_sessions` tool
// serialize a different struct; the point of the pairing is that the one rule
// holds on both, so a consumer does not have to learn which payload it is in.
func TestAISessionJSONFollowsTheSameNeverSetRule(t *testing.T) {
	encoded, err := json.Marshal(model.AISessionStatus{SessionID: "s-1", State: model.AISessionStateIdle, Reason: "recorded"})
	if err != nil {
		t.Fatalf("marshal ai session status: %v", err)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Errorf("a session with no recorded activity is rendered with Go's zero instant instead of omitting the field:\n%s", encoded)
	}

	seen := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	encoded, err = json.Marshal(model.AISessionStatus{SessionID: "s-1", State: model.AISessionStateIdle, Reason: "recorded", LastActivity: seen})
	if err != nil {
		t.Fatalf("marshal ai session status: %v", err)
	}
	if !strings.Contains(string(encoded), "2026-09-22T10:30:00Z") {
		t.Errorf("a recorded last activity must still serialize:\n%s", encoded)
	}
}
