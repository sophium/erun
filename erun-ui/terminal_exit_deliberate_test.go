package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A deliberate close kills PTYs, so their exits must be marked deliberate
// rather than rendered as failures; exits nothing caused stay unmarked.

// exitPayloadFor returns the single terminal-exit payload finalizeSessionExit
// emits.
func exitPayloadFor(t *testing.T, app *App, managed *managedTerminal, reason string) terminalExitPayload {
	t.Helper()
	emits := newCapturedEmits()
	app.emitFn = emits.fn()
	app.finalizeSessionExit(managed, reason)
	events := emits.events(uimodel.TerminalExitEvent)
	if len(events) != 1 {
		t.Fatalf("expected 1 %s event, got %d (%+v)", uimodel.TerminalExitEvent, len(events), events)
	}
	payload, ok := events[0].(terminalExitPayload)
	if !ok {
		t.Fatalf("expected a terminalExitPayload, got %T", events[0])
	}
	return payload
}

// The mark comes from the close itself: it can error after killing everything
// it reached.
func TestACloseReportsTheExitsItKilledAsItsOwn(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	target := uimodel.Selection{Tenant: "erun", Environment: "local"}
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

func TestASessionThatDiesOnItsOwnIsNotMarkedDeliberate(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	managed := &managedTerminal{
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
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

// The mark is set before the kill, so a Close reporting the process survived
// (as on Windows) withdraws it.

// survivingTerminalSession outlives its Close; Alive stays true.
type survivingTerminalSession struct{ *stubTerminalSession }

func newSurvivingTerminalSession() *survivingTerminalSession {
	return &survivingTerminalSession{stubTerminalSession: newStubTerminalSession()}
}

func (s *survivingTerminalSession) Close() error {
	return fmt.Errorf("%w: pid 4242", errKillDidNotLand)
}

// managedWithSession seeds one managed session on an app that owns it.
func managedWithSession(app *App, session terminalSession, serial int) *managedTerminal {
	managed := &managedTerminal{
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
		session:   session,
		serial:    serial,
		key:       fmt.Sprintf("erun-local-%d", serial),
	}
	app.sessions[managed.key] = managed
	return managed
}

func TestACloseThatCouldNotKillDoesNotOwnTheSurvivorsExit(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	managed := managedWithSession(app, newSurvivingTerminalSession(), 43)

	if _, err := app.CloseEnvironmentSessions(selection); !errors.Is(err, errKillDidNotLand) {
		t.Fatalf("a close that did not end its session must say so, got err=%v", err)
	}

	payload := exitPayloadFor(t, app, managed, "exit status 1")
	if payload.Deliberate {
		t.Fatal("a session the close could not kill must not have its later death reported as the close's own")
	}
}

func TestACloseThatCouldNotKillDoesNotOwnTheSurvivorsExitOnEveryTeardownPath(t *testing.T) {
	for _, test := range []struct {
		name  string
		close func(app *App, managed *managedTerminal) error
	}{
		{
			name: "closeManaged",
			close: func(app *App, managed *managedTerminal) error {
				return app.closeManaged(managed)
			},
		},
		{
			name: "closeAllSessionsLocked",
			close: func(app *App, managed *managedTerminal) error {
				app.closeAllSessionsLocked()
				return nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &App{sessions: make(map[string]*managedTerminal)}
			managed := managedWithSession(app, newSurvivingTerminalSession(), 44)

			_ = test.close(app, managed)

			payload := exitPayloadFor(t, app, managed, "exit status 1")
			if payload.Deliberate {
				t.Fatal("a teardown that did not kill must not answer for the survivor's death")
			}
		})
	}
}

// A Close that killed the process but failed to release the pty keeps the claim.
func TestACloseThatCouldNotReleaseItsPtyStillOwnsItsExit(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	session := &ptyReleaseFailureSession{stubTerminalSession: newStubTerminalSession()}
	managed := managedWithSession(app, session, 45)

	if _, err := app.CloseEnvironmentSessions(selection); err == nil {
		t.Fatal("the pty release failure must still reach the caller")
	}

	payload := exitPayloadFor(t, app, managed, "signal: killed")
	if !payload.Deliberate {
		t.Fatal("a kill that landed owns its exit, whatever the desktop could not release afterwards")
	}
}

// ptyReleaseFailureSession ended its process and then failed to release the
// desktop's read side of it.
type ptyReleaseFailureSession struct{ *stubTerminalSession }

func (s *ptyReleaseFailureSession) Close() error {
	close(s.closeCh)
	return errors.New("close |0: bad file descriptor")
}

// The handle, not taskkill's status, decides whether the process survived.
func TestTheKillIsJudgedByWhatItObserved(t *testing.T) {
	for _, test := range []struct {
		name       string
		killErr    error
		alive      bool
		wantMissed bool
	}{
		{
			name:    "a failed kill on a process already gone",
			killErr: errors.New("exit status 128"),
			alive:   false,
		},
		{
			name:       "a failed kill on a live process",
			killErr:    errors.New("Access is denied."),
			alive:      true,
			wantMissed: true,
		},
		{
			name:       "a kill that reported success on a live process",
			killErr:    nil,
			alive:      true,
			wantMissed: true,
		},
		{
			name:    "a kill that reported success on a dead process",
			killErr: nil,
			alive:   false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := killProcessTree(4242, func() error { return test.killErr }, func() bool { return test.alive })
			if missed := errors.Is(err, errKillDidNotLand); missed != test.wantMissed {
				t.Fatalf("survivor reported = %v, want %v (err=%v)", missed, test.wantMissed, err)
			}
		})
	}

	t.Run("no pid, nothing to kill", func(t *testing.T) {
		ran := false
		if err := killProcessTree(0, func() error { ran = true; return nil }, func() bool { return true }); err != nil {
			t.Fatalf("a session with no process behind it has nothing to report: %v", err)
		}
		if ran {
			t.Fatal("a session with no pid must not run a kill at all")
		}
	})
}
