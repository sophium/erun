package activity

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func recordAISessionEventForTest(t *testing.T, params model.AISessionEventParams) {
	t.Helper()
	if err := RecordAISessionEvent(params); err != nil {
		t.Fatalf("record %s: %v", params.Event, err)
	}
}

func loadAISessionStatusForTest(t *testing.T, tenant, environment, sessionID string) model.AISessionStatus {
	t.Helper()
	status, err := LoadAISessionStatus(tenant, environment, sessionID)
	if err != nil {
		t.Fatalf("load status for %s: %v", sessionID, err)
	}
	return status
}

// A session waiting on the human is silent, which an output-volume heuristic reads as idle.
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

	// The model has no elapsed-time input, so silence can never decay to Idle.
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
