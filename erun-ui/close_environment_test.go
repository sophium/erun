package main

import (
	"testing"
)

// TestCloseEnvironmentSessionsClosesEveryMatchingSession is the
// regression: the sidebar's "close env" dot must
// tear down every PTY bound to (tenant, env) — Local + ERun + AI
// + any extra terminals the user spawned — not just one. The
// returned slice must list the serial IDs that were closed so the
// frontend can drop them from tabsByEnv in one round-trip.
func TestCloseEnvironmentSessionsClosesEveryMatchingSession(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	target := uiSelection{Tenant: "erun", Environment: "local"}
	stub1 := newStubTerminalSession()
	stub2 := newStubTerminalSession()
	stub3 := newStubTerminalSession()
	app.sessions["erun-local-1"] = &managedTerminal{selection: target, session: stub1, serial: 11, key: "erun-local-1"}
	app.sessions["erun-local-2"] = &managedTerminal{selection: target, session: stub2, serial: 12, key: "erun-local-2"}
	app.sessions["erun-local-3"] = &managedTerminal{selection: target, session: stub3, serial: 13, key: "erun-local-3"}

	closed, err := app.CloseEnvironmentSessions(target)
	if err != nil {
		t.Fatalf("CloseEnvironmentSessions returned err: %v", err)
	}
	if len(closed) != 3 {
		t.Fatalf("expected 3 sessions closed, got %d (%v)", len(closed), closed)
	}
	for _, stub := range []*stubTerminalSession{stub1, stub2, stub3} {
		select {
		case <-stub.closeCh:
		default:
			t.Fatalf("stub session was not Closed()")
		}
	}
}

// TestCloseEnvironmentSessionsLeavesOtherEnvsAlone guards against a
// fat-fingered match predicate quietly tearing down a different env
// (e.g. ignoring tenant, matching only by env name).
func TestCloseEnvironmentSessionsLeavesOtherEnvsAlone(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	target := uiSelection{Tenant: "erun", Environment: "local"}
	sameEnvDifferentTenant := uiSelection{Tenant: "petios", Environment: "local"}
	sameTenantDifferentEnv := uiSelection{Tenant: "erun", Environment: "dev"}

	targetStub := newStubTerminalSession()
	otherStub := newStubTerminalSession()
	siblingStub := newStubTerminalSession()
	app.sessions["target"] = &managedTerminal{selection: target, session: targetStub, serial: 1, key: "target"}
	app.sessions["other-tenant"] = &managedTerminal{selection: sameEnvDifferentTenant, session: otherStub, serial: 2, key: "other-tenant"}
	app.sessions["sibling"] = &managedTerminal{selection: sameTenantDifferentEnv, session: siblingStub, serial: 3, key: "sibling"}

	closed, err := app.CloseEnvironmentSessions(target)
	if err != nil {
		t.Fatalf("CloseEnvironmentSessions returned err: %v", err)
	}
	if len(closed) != 1 || closed[0] != 1 {
		t.Fatalf("expected only serial 1 closed, got %v", closed)
	}
	for name, stub := range map[string]*stubTerminalSession{"other-tenant": otherStub, "sibling": siblingStub} {
		select {
		case <-stub.closeCh:
			t.Fatalf("unrelated session %s was closed", name)
		default:
		}
	}
}

// TestCloseEnvironmentSessionsSkipsAlreadyClosed guards the
// idempotency the frontend relies on: clicking the dot a second
// time after the underlying sessions have already exited (via PTY
// crash, manual close-tab, etc.) must not return an error and must
// not double-close a session.
func TestCloseEnvironmentSessionsSkipsAlreadyClosed(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	target := uiSelection{Tenant: "erun", Environment: "local"}
	stub := newStubTerminalSession()
	app.sessions["one"] = &managedTerminal{selection: target, session: stub, serial: 1, key: "one", closed: true}

	closed, err := app.CloseEnvironmentSessions(target)
	if err != nil {
		t.Fatalf("CloseEnvironmentSessions returned err: %v", err)
	}
	if len(closed) != 0 {
		t.Fatalf("expected no sessions closed (already marked closed), got %v", closed)
	}
	select {
	case <-stub.closeCh:
		t.Fatalf("closed-flag session was Close()'d again")
	default:
	}
}

// TestCloseEnvironmentSessionsRejectsEmptyTarget keeps the API
// contract narrow: the desktop must resolve a tenant/env pair
// before asking the backend to tear down sessions. A bug that
// passes an empty selection should fail loudly rather than
// silently iterating over every session.
func TestCloseEnvironmentSessionsRejectsEmptyTarget(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	if _, err := app.CloseEnvironmentSessions(uiSelection{}); err == nil {
		t.Fatal("expected error when tenant and environment are both empty")
	}
	if _, err := app.CloseEnvironmentSessions(uiSelection{Tenant: "erun"}); err == nil {
		t.Fatal("expected error when environment is empty")
	}
}
