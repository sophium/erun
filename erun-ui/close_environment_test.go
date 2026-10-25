package main

import (
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestCloseEnvironmentSessionsClosesEveryMatchingSession(t *testing.T) {
	app := withTestEnvops(&App{sessions: make(map[string]*managedTerminal)})
	target := uimodel.Selection{Tenant: "erun", Environment: "local"}
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

func TestCloseEnvironmentSessionsLeavesOtherEnvsAlone(t *testing.T) {
	app := withTestEnvops(&App{sessions: make(map[string]*managedTerminal)})
	target := uimodel.Selection{Tenant: "erun", Environment: "local"}
	sameEnvDifferentTenant := uimodel.Selection{Tenant: "petios", Environment: "local"}
	sameTenantDifferentEnv := uimodel.Selection{Tenant: "erun", Environment: "dev"}

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

func TestCloseEnvironmentSessionsSkipsAlreadyClosed(t *testing.T) {
	app := withTestEnvops(&App{sessions: make(map[string]*managedTerminal)})
	target := uimodel.Selection{Tenant: "erun", Environment: "local"}
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

func TestCloseEnvironmentSessionsRejectsEmptyTarget(t *testing.T) {
	app := withTestEnvops(&App{sessions: make(map[string]*managedTerminal)})
	if _, err := app.CloseEnvironmentSessions(uimodel.Selection{}); err == nil {
		t.Fatal("expected error when tenant and environment are both empty")
	}
	if _, err := app.CloseEnvironmentSessions(uimodel.Selection{Tenant: "erun"}); err == nil {
		t.Fatal("expected error when environment is empty")
	}
}

// Closing must mark sessions closed so tryReconnect refuses and the AI busy latch clears.
func TestCloseEnvironmentSessionsMarksClosedSoReconnectRefuses(t *testing.T) {
	app := withTestEnvops(&App{
		sessions: make(map[string]*managedTerminal),
		busyEnvs: make(map[string]int),
	})
	target := uimodel.Selection{Tenant: "erun", Environment: "local"}
	// A non-nil respawn means only the closed flag can make tryReconnect refuse.
	ai := &managedTerminal{
		selection:      target,
		session:        newStubTerminalSession(),
		serial:         1,
		key:            "ai",
		kind:           sessionKindAI,
		aiBusyEmitted:  true,
		blocksIdleStop: true,
		respawn:        func() (terminalSession, error) { return newStubTerminalSession(), nil },
	}
	app.sessions["ai"] = ai
	app.busyEnvs[selectionKey(target)] = 1

	if _, err := app.CloseEnvironmentSessions(target); err != nil {
		t.Fatalf("CloseEnvironmentSessions returned err: %v", err)
	}
	if !ai.closed {
		t.Fatal("close must mark the session closed so tryReconnect refuses it")
	}
	if _, ok := app.busyEnvs[selectionKey(target)]; ok {
		t.Fatal("close must release the env's idle-stop block")
	}
	if app.tryReconnect(ai, "test") {
		t.Fatal("tryReconnect must refuse a session closed by CloseEnvironmentSessions")
	}
}
