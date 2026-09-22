package eruncommon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIdleJSONOmitsTimestampsThatWereNeverSet pins the representation the idle
// payload owes a machine consumer.
//
// `erun idle --output json` shipped fields typed as timestamps that carried
// Go's zero instant (`0001-01-01T00:00:00Z`) whenever the marker behind them
// had never been touched. A consumer cannot tell that apart from a marker seen
// at the zero instant without knowing Go's sentinel, and one payload carried
// it four times over. Every one of those fields is declared with a tag whose
// name says "omit when unset" -- the tag simply does not do that for a struct,
// which is the whole bug: `omitempty` has no empty case for time.Time, so the
// declared intent and the wire form had never matched.
//
// The rule asserted here is the one a consumer should need: a timestamp that
// was never set is absent, and a timestamp that was set is present and
// unchanged. It is asserted over the same shapes `erun idle` serializes --
// the `activity` snapshot per kind and the markers built from them -- so the
// convention cannot hold on one and not the other.
func TestIdleJSONOmitsTimestampsThatWereNeverSet(t *testing.T) {
	seen := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	status := EnvironmentIdleStatus{
		Policy:   EnvironmentIdlePolicy{Timeout: time.Hour},
		Markers:  []EnvironmentIdleMarker{NeverTouchedIdleMarker("ssh")},
		Activity: map[string]EnvironmentActivitySnapshot{"ssh": {}},
	}

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal idle status: %v", err)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Errorf("a never-set timestamp is rendered as Go's zero instant instead of being absent:\n%s", encoded)
	}

	// The other direction: a timestamp that *was* set must still serialize, or
	// the fix would have traded a wrong payload for an empty one.
	status.Markers = append(status.Markers, EnvironmentIdleMarker{
		Name:         "api",
		LastActivity: seen,
		LastSeen:     seen,
		Clients:      []EnvironmentIdleMarkerClient{{Address: "127.0.0.1:46572", LastActivity: seen, SecondsAgo: 12}},
	})
	status.Activity["api"] = EnvironmentActivitySnapshot{LastActivity: seen, LastSeen: seen}

	encoded, err = json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal idle status: %v", err)
	}
	payload := string(encoded)
	if got := strings.Count(payload, "2026-09-22T10:30:00Z"); got != 5 {
		t.Errorf("expected every set timestamp to survive serialization (activity lastActivity/lastSeen, marker lastActivity/lastSeen, client lastActivity = 5), got %d:\n%s", got, payload)
	}
}

// TestAISessionJSONFollowsTheSameNeverSetRule covers the sibling surface that
// carried the identical tag: an AI session that has recorded nothing is not a
// session whose last activity was the zero instant either. It is asserted
// separately because `erun activity ai-session` and the MCP `ai_sessions` tool
// serialize a different struct; the point of the pairing is that the one rule
// holds on both, so a consumer does not have to learn which payload it is in.
func TestAISessionJSONFollowsTheSameNeverSetRule(t *testing.T) {
	encoded, err := json.Marshal(AISessionStatus{SessionID: "s-1", State: AISessionStateIdle, Reason: "recorded"})
	if err != nil {
		t.Fatalf("marshal ai session status: %v", err)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Errorf("a session with no recorded activity is rendered with Go's zero instant instead of omitting the field:\n%s", encoded)
	}

	seen := time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC)
	encoded, err = json.Marshal(AISessionStatus{SessionID: "s-1", State: AISessionStateIdle, Reason: "recorded", LastActivity: seen})
	if err != nil {
		t.Fatalf("marshal ai session status: %v", err)
	}
	if !strings.Contains(string(encoded), "2026-09-22T10:30:00Z") {
		t.Errorf("a recorded last activity must still serialize:\n%s", encoded)
	}
}

// NeverTouchedIdleMarker builds the marker `erun idle` reports for an activity
// kind that has never been recorded: every timestamp behind it is the zero
// time, which is exactly the state the zero-instant leak made unreadable.
func NeverTouchedIdleMarker(name string) EnvironmentIdleMarker {
	return EnvironmentIdleMarker{
		Name:         name,
		Idle:         true,
		Reason:       "no activity recorded for this marker",
		Clients:      []EnvironmentIdleMarkerClient{{Address: "127.0.0.1:46572"}},
		LastSeen:     time.Time{},
		LastActivity: time.Time{},
	}
}
