package activity

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// recordAISessionEventForTest records an event and fails the test on error,
// keeping the scenario steps below to one line each.
func recordAISessionEventForTest(t *testing.T, params model.AISessionEventParams) {
	t.Helper()
	if err := RecordAISessionEvent(params); err != nil {
		t.Fatalf("record %s: %v", params.Event, err)
	}
}

// loadAISessionStatusForTest resolves a status and fails the test on error.
func loadAISessionStatusForTest(t *testing.T, tenant, environment, sessionID string) model.AISessionStatus {
	t.Helper()
	status, err := LoadAISessionStatus(tenant, environment, sessionID)
	if err != nil {
		t.Fatalf("load status for %s: %v", sessionID, err)
	}
	return status
}

// TestAISessionAwaitingInputIsNotIdleDuringLongSilence is the load-bearing
// case a PTY output-volume heuristic cannot represent: a session that
// finished its turn and is waiting on the human produces no output at all,
// which a silence-based heuristic reads identically to "idle" or "finished".
// The status model must read it as AwaitingInput regardless of how long the
// silence runs, and it must stay distinguishable from a session that has
// actually exited.
func TestAISessionAwaitingInputIsNotIdleDuringLongSilence(t *testing.T) {
	isolateActivityCache(t)
	tenant, environment, sessionID := "acme", "dev", "session-1"

	t.Run("turn-start reads as busy", func(t *testing.T) {
		recordAISessionEventForTest(t, model.AISessionEventParams{
			Tenant: tenant, Environment: environment, SessionID: sessionID,
			Tool: "claude", Event: model.AISessionEventTurnStart,
		})
		if got := loadAISessionStatusForTest(t, tenant, environment, sessionID); got.State != model.AISessionStateBusy {
			t.Fatalf("after turn-start: want busy, got %s", got.State)
		}
	})

	// No further events arrive after turn-end - the session is silent
	// because it is waiting on the human, not because it went away. This
	// must not decay into Idle no matter how long the silence runs (there is
	// no elapsed-time input to this model at all, so "how long" isn't even
	// simulated here - the absence of a timeout is the point).
	t.Run("turn-end reads as awaiting-input and carries the tool forward", func(t *testing.T) {
		recordAISessionEventForTest(t, model.AISessionEventParams{
			Tenant: tenant, Environment: environment, SessionID: sessionID,
			Event: model.AISessionEventTurnEnd,
		})
		got := loadAISessionStatusForTest(t, tenant, environment, sessionID)
		if got.State != model.AISessionStateAwaitingInput {
			t.Fatalf("after turn-end with no further output: want awaiting-input, got %s (reason %q)", got.State, got.Reason)
		}
		if got.Tool != "claude" {
			t.Fatalf("tool should carry forward from the earlier event, got %q", got.Tool)
		}
	})
}

func TestRecordAISessionEventRejectsPathTraversalSessionID(t *testing.T) {
	err := RecordAISessionEvent(model.AISessionEventParams{
		Tenant: "acme", Environment: "dev", SessionID: "../escape", Event: model.AISessionEventTurnStart,
	})
	if err == nil {
		t.Fatalf("expected an error for a path-traversal session id")
	}
}

func TestRecordAISessionEventRequiresTenantAndEnvironment(t *testing.T) {
	if err := RecordAISessionEvent(model.AISessionEventParams{SessionID: "s", Event: model.AISessionEventTurnStart}); err == nil {
		t.Fatalf("expected an error for missing tenant/environment")
	}
}
