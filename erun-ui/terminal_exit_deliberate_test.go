package main

import "testing"

// A close the operator asked for kills the env's PTYs on purpose, so the
// session's own reader wakes with a non-empty reason ("signal: killed") for a
// kill that worked. The desktop needs to know that is what happened before it
// renders it as "Failed to open <tenant> / <env>: signal: killed", and the
// session is the only thing that can say so: the set CloseEnvironmentSessions
// set out to walk is not the set it reached (closeManagedTerminals gives up on
// the first Close that fails and returns its error with the rest already
// dead), and a session it kills before the frontend has recorded a tab for it
// is named nowhere else. These two tests pin the flag it is answered with and
// the one case it must stay off for: an exit nothing deliberate caused.

// exitPayloadFor runs the teardown's own exit through finalizeSessionExit and
// returns the single terminal-exit payload it emitted.
func exitPayloadFor(t *testing.T, app *App, managed *managedTerminal, reason string) terminalExitPayload {
	t.Helper()
	emits := newCapturedEmits()
	app.emitFn = emits.fn()
	app.finalizeSessionExit(managed, reason)
	events := emits.events(terminalExitEvent)
	if len(events) != 1 {
		t.Fatalf("expected 1 %s event, got %d (%+v)", terminalExitEvent, len(events), events)
	}
	payload, ok := events[0].(terminalExitPayload)
	if !ok {
		t.Fatalf("expected a terminalExitPayload, got %T", events[0])
	}
	return payload
}

// TestACloseReportsTheExitsItKilledAsItsOwn drives the real close: the desktop
// marks each session it is about to tear down under its lock, and the exit that
// session's reader produces afterwards carries that mark. The mark has to come
// from the close rather than from the size of its answer, because the close can
// come back an error having killed everything it reached.
func TestACloseReportsTheExitsItKilledAsItsOwn(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	target := uiSelection{Tenant: "erun", Environment: "local"}
	managed := &managedTerminal{
		selection: target,
		session:   newStubTerminalSession(),
		serial:    41,
		key:       "erun-local-41",
	}
	app.sessions[managed.key] = managed

	if _, err := app.CloseEnvironmentSessions(target); err != nil {
		t.Fatalf("CloseEnvironmentSessions returned err: %v", err)
	}

	payload := exitPayloadFor(t, app, managed, "signal: killed")
	if !payload.Deliberate {
		t.Fatal("the exit of a session the close tore down must be reported as the close's own")
	}
	if payload.SessionID != 41 {
		t.Fatalf("expected the session's own serial, got %d", payload.SessionID)
	}
}

// TestASessionThatDiesOnItsOwnIsNotMarkedDeliberate is the other half of that
// contract, and the one that keeps the flag from swallowing real failures: a
// session nothing tore down exits with the flag off, so the desktop still
// reports what killed it — whether or not a close is in flight elsewhere.
func TestASessionThatDiesOnItsOwnIsNotMarkedDeliberate(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	managed := &managedTerminal{
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		session:   newStubTerminalSession(),
		serial:    42,
		key:       "erun-local-42",
	}
	app.sessions[managed.key] = managed

	payload := exitPayloadFor(t, app, managed, "exit status 1")
	if payload.Deliberate {
		t.Fatal("a session that died on its own must not be reported as a deliberate teardown")
	}
}
