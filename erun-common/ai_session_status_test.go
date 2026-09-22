package eruncommon

import (
	"encoding/json"
	"testing"
	"time"
)

// recordAISessionEventForTest records an event and fails the test on error,
// keeping the scenario steps below to one line each.
func recordAISessionEventForTest(t *testing.T, params AISessionEventParams) {
	t.Helper()
	if err := RecordAISessionEvent(params); err != nil {
		t.Fatalf("record %s: %v", params.Event, err)
	}
}

// loadAISessionStatusForTest resolves a status and fails the test on error.
func loadAISessionStatusForTest(t *testing.T, tenant, environment, sessionID string) AISessionStatus {
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
		recordAISessionEventForTest(t, AISessionEventParams{
			Tenant: tenant, Environment: environment, SessionID: sessionID,
			Tool: "claude", Event: AISessionEventTurnStart,
		})
		if got := loadAISessionStatusForTest(t, tenant, environment, sessionID); got.State != AISessionStateBusy {
			t.Fatalf("after turn-start: want busy, got %s", got.State)
		}
	})

	// No further events arrive after turn-end - the session is silent
	// because it is waiting on the human, not because it went away. This
	// must not decay into Idle no matter how long the silence runs (there is
	// no elapsed-time input to this model at all, so "how long" isn't even
	// simulated here - the absence of a timeout is the point).
	t.Run("turn-end reads as awaiting-input and carries the tool forward", func(t *testing.T) {
		recordAISessionEventForTest(t, AISessionEventParams{
			Tenant: tenant, Environment: environment, SessionID: sessionID,
			Event: AISessionEventTurnEnd,
		})
		got := loadAISessionStatusForTest(t, tenant, environment, sessionID)
		if got.State != AISessionStateAwaitingInput {
			t.Fatalf("after turn-end with no further output: want awaiting-input, got %s (reason %q)", got.State, got.Reason)
		}
		if got.Tool != "claude" {
			t.Fatalf("tool should carry forward from the earlier event, got %q", got.Tool)
		}
	})

	t.Run("a session that never reported anything is idle, not awaiting-input", func(t *testing.T) {
		awaiting := loadAISessionStatusForTest(t, tenant, environment, sessionID)
		idle := loadAISessionStatusForTest(t, tenant, environment, "never-started")
		if idle.State != AISessionStateIdle {
			t.Fatalf("session with no recorded event: want idle, got %s", idle.State)
		}
		if awaiting.State == idle.State {
			t.Fatalf("awaiting-input must not collapse into idle")
		}
	})

	t.Run("exit reads as exited, distinct from awaiting-input and idle", func(t *testing.T) {
		exitCode := 0
		recordAISessionEventForTest(t, AISessionEventParams{
			Tenant: tenant, Environment: environment, SessionID: sessionID,
			Event: AISessionEventExit, ExitCode: &exitCode,
		})
		got := loadAISessionStatusForTest(t, tenant, environment, sessionID)
		if got.State != AISessionStateExited {
			t.Fatalf("after exit: want exited, got %s", got.State)
		}
		if got.State == AISessionStateAwaitingInput || got.State == AISessionStateIdle {
			t.Fatalf("exited must be distinguishable from both awaiting-input and idle")
		}
	})
}

func TestAISessionNotifyResolvesToAwaitingInput(t *testing.T) {
	record := AISessionRecord{SessionID: "s", Event: AISessionEventNotify, At: time.Now()}
	status := ResolveAISessionStatus(record)
	if status.State != AISessionStateAwaitingInput {
		t.Fatalf("notify event: want awaiting-input, got %s", status.State)
	}
}

func TestAISessionOOMExitIsDistinctFromPlainExit(t *testing.T) {
	isolateActivityCache(t)
	tenant, environment := "acme", "dev"

	recordAISessionEventForTest(t, AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "oom-session",
		Event: AISessionEventExit, ExitReason: AISessionExitReasonOOM,
	})
	oom := loadAISessionStatusForTest(t, tenant, environment, "oom-session")
	if oom.State != AISessionStateOOMKilled {
		t.Fatalf("exit with oom reason: want oom-killed, got %s", oom.State)
	}

	exitCode := 1
	recordAISessionEventForTest(t, AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "plain-exit-session",
		Event: AISessionEventExit, ExitCode: &exitCode,
	})
	plain := loadAISessionStatusForTest(t, tenant, environment, "plain-exit-session")
	if plain.State != AISessionStateExited {
		t.Fatalf("plain exit: want exited, got %s", plain.State)
	}
	if plain.State == oom.State {
		t.Fatalf("an OOM kill must be distinguishable from an ordinary exit")
	}
}

func TestLoadAISessionStatusesListsAllRecordedSessions(t *testing.T) {
	isolateActivityCache(t)
	tenant, environment := "acme", "dev-list"
	recordAISessionEventForTest(t, AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "b-session",
		Tool: "codex", Event: AISessionEventTurnStart,
	})
	recordAISessionEventForTest(t, AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "a-session",
		Tool: "claude", Event: AISessionEventTurnEnd,
	})

	statuses, err := LoadAISessionStatuses(tenant, environment)
	if err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(statuses))
	}
	if statuses[0].SessionID != "a-session" || statuses[1].SessionID != "b-session" {
		t.Fatalf("statuses should sort by session id, got %+v", statuses)
	}
	if statuses[0].State != AISessionStateAwaitingInput {
		t.Fatalf("a-session should be awaiting-input, got %s", statuses[0].State)
	}
	if statuses[1].State != AISessionStateBusy {
		t.Fatalf("b-session should be busy, got %s", statuses[1].State)
	}
}

// TestLoadAISessionStatusesEmptyWhenNoneRecordedSerializesAsEmptyArray pins
// the JSON shape a caller actually observes, not just len(statuses) == 0: a
// nil slice and an empty slice both report length zero, but only the empty
// slice marshals to "[]" rather than "null". A caller doing
// result.sessions.length or ranging over the field would otherwise have to
// special-case null for this one tool while every sibling collection-typed
// tool (e.g. idle_stop_history's entries) already returns "[]".
func TestLoadAISessionStatusesEmptyWhenNoneRecordedSerializesAsEmptyArray(t *testing.T) {
	isolateActivityCache(t)
	statuses, err := LoadAISessionStatuses("acme", "never-touched-env")
	if err != nil {
		t.Fatalf("load statuses for untouched env: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("want no sessions, got %+v", statuses)
	}
	if statuses == nil {
		t.Fatalf("want a non-nil empty slice so JSON marshals to [], got a nil slice which marshals to null")
	}
	data, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("marshal empty statuses: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("want empty statuses to marshal as [], got %s", data)
	}
}

// TestLoadAISessionStatusesNonEmptySerializesAsArray confirms the fix above
// does not disturb the populated case: a real listing must still marshal as
// a JSON array of the resolved statuses.
func TestLoadAISessionStatusesNonEmptySerializesAsArray(t *testing.T) {
	isolateActivityCache(t)
	tenant, environment := "acme", "dev-list-json"
	recordAISessionEventForTest(t, AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "a-session",
		Tool: "claude", Event: AISessionEventTurnStart,
	})

	statuses, err := LoadAISessionStatuses(tenant, environment)
	if err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	data, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("marshal statuses: %v", err)
	}
	var roundTripped []AISessionStatus
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal statuses: %v", err)
	}
	if len(roundTripped) != 1 || roundTripped[0].SessionID != "a-session" {
		t.Fatalf("want one round-tripped session a-session, got %+v", roundTripped)
	}
}

func TestRecordAISessionEventRejectsUnsupportedEventKind(t *testing.T) {
	err := RecordAISessionEvent(AISessionEventParams{
		Tenant: "acme", Environment: "dev", SessionID: "s", Event: AISessionEventKind("bogus"),
	})
	if err == nil {
		t.Fatalf("expected an error for an unsupported event kind")
	}
}

func TestRecordAISessionEventRejectsPathTraversalSessionID(t *testing.T) {
	err := RecordAISessionEvent(AISessionEventParams{
		Tenant: "acme", Environment: "dev", SessionID: "../escape", Event: AISessionEventTurnStart,
	})
	if err == nil {
		t.Fatalf("expected an error for a path-traversal session id")
	}
}

func TestRecordAISessionEventRequiresTenantAndEnvironment(t *testing.T) {
	if err := RecordAISessionEvent(AISessionEventParams{SessionID: "s", Event: AISessionEventTurnStart}); err == nil {
		t.Fatalf("expected an error for missing tenant/environment")
	}
}
