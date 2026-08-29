package eruncommon

import (
	"testing"
	"time"
)

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
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: sessionID,
		Tool: "claude", Event: AISessionEventTurnStart, Now: start,
	}); err != nil {
		t.Fatalf("record turn-start: %v", err)
	}
	busy, err := LoadAISessionStatus(tenant, environment, sessionID, start)
	if err != nil {
		t.Fatalf("load after turn-start: %v", err)
	}
	if busy.State != AISessionStateBusy {
		t.Fatalf("after turn-start: want busy, got %s", busy.State)
	}

	turnEnd := start.Add(2 * time.Minute)
	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: sessionID,
		Event: AISessionEventTurnEnd, Now: turnEnd,
	}); err != nil {
		t.Fatalf("record turn-end: %v", err)
	}

	// No further events arrive - the session is silent because it is
	// waiting on the human, not because it went away. An hour of silence
	// must not decay this into Idle.
	muchLater := turnEnd.Add(1 * time.Hour)
	awaiting, err := LoadAISessionStatus(tenant, environment, sessionID, muchLater)
	if err != nil {
		t.Fatalf("load during silence: %v", err)
	}
	if awaiting.State != AISessionStateAwaitingInput {
		t.Fatalf("during long silence after turn-end: want awaiting-input, got %s (reason %q)", awaiting.State, awaiting.Reason)
	}
	if awaiting.Tool != "claude" {
		t.Fatalf("tool should carry forward from the earlier event, got %q", awaiting.Tool)
	}

	// A session that never reported anything is genuinely Idle, and must not
	// be confused with one that is silently awaiting input.
	idle, err := LoadAISessionStatus(tenant, environment, "never-started", muchLater)
	if err != nil {
		t.Fatalf("load unknown session: %v", err)
	}
	if idle.State != AISessionStateIdle {
		t.Fatalf("session with no recorded event: want idle, got %s", idle.State)
	}
	if awaiting.State == idle.State {
		t.Fatalf("awaiting-input must not collapse into idle")
	}

	// Once the process actually exits, the state changes again and must not
	// be confused with the awaiting-input state that preceded it.
	exitCode := 0
	exitAt := muchLater.Add(time.Minute)
	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: sessionID,
		Event: AISessionEventExit, ExitCode: &exitCode, Now: exitAt,
	}); err != nil {
		t.Fatalf("record exit: %v", err)
	}
	exited, err := LoadAISessionStatus(tenant, environment, sessionID, exitAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("load after exit: %v", err)
	}
	if exited.State != AISessionStateExited {
		t.Fatalf("after exit: want exited, got %s", exited.State)
	}
	if exited.State == AISessionStateAwaitingInput || exited.State == AISessionStateIdle {
		t.Fatalf("exited must be distinguishable from both awaiting-input and idle")
	}
}

func TestAISessionNotifyResolvesToAwaitingInput(t *testing.T) {
	record := AISessionRecord{SessionID: "s", Event: AISessionEventNotify, At: time.Now()}
	status := ResolveAISessionStatus(record, time.Now())
	if status.State != AISessionStateAwaitingInput {
		t.Fatalf("notify event: want awaiting-input, got %s", status.State)
	}
}

func TestAISessionOOMExitIsDistinctFromPlainExit(t *testing.T) {
	isolateActivityCache(t)
	tenant, environment := "acme", "dev"
	now := time.Now()

	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "oom-session",
		Event: AISessionEventExit, ExitReason: AISessionExitReasonOOM, Now: now,
	}); err != nil {
		t.Fatalf("record oom exit: %v", err)
	}
	oom, err := LoadAISessionStatus(tenant, environment, "oom-session", now)
	if err != nil {
		t.Fatalf("load oom session: %v", err)
	}
	if oom.State != AISessionStateOOMKilled {
		t.Fatalf("exit with oom reason: want oom-killed, got %s", oom.State)
	}

	exitCode := 1
	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "plain-exit-session",
		Event: AISessionEventExit, ExitCode: &exitCode, Now: now,
	}); err != nil {
		t.Fatalf("record plain exit: %v", err)
	}
	plain, err := LoadAISessionStatus(tenant, environment, "plain-exit-session", now)
	if err != nil {
		t.Fatalf("load plain-exit session: %v", err)
	}
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
	now := time.Now()
	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "b-session",
		Tool: "codex", Event: AISessionEventTurnStart, Now: now,
	}); err != nil {
		t.Fatalf("record b-session: %v", err)
	}
	if err := RecordAISessionEvent(AISessionEventParams{
		Tenant: tenant, Environment: environment, SessionID: "a-session",
		Tool: "claude", Event: AISessionEventTurnEnd, Now: now,
	}); err != nil {
		t.Fatalf("record a-session: %v", err)
	}

	statuses, err := LoadAISessionStatuses(tenant, environment, now)
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

func TestLoadAISessionStatusesEmptyWhenNoneRecorded(t *testing.T) {
	isolateActivityCache(t)
	statuses, err := LoadAISessionStatuses("acme", "never-touched-env", time.Now())
	if err != nil {
		t.Fatalf("load statuses for untouched env: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("want no sessions, got %+v", statuses)
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
