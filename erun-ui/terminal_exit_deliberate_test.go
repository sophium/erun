package main

import (
	"errors"
	"fmt"
	"testing"
)

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

// The mark above is a claim, and the teardown that made it can be wrong: it is
// set before the PTY is touched, so it is in place for a session killed the
// instant the kill lands, which means it is also in place for a session the
// kill never reached. Windows is where that happens — taskkill's own failure was
// discarded, so a Close there came back looking like every other one while the
// process it was asked to end was still running — and what the tests below pin
// is the report that closes the gap: a Close that says the process outlived the
// kill takes the claim back, and the survivor's real death is reported.
//
// The platform seam is Close's own error. These run on a host that cannot
// execute session_windows.go at all, so what they reproduce is the decision the
// Windows path now feeds — the same decision every platform's teardown goes
// through, exercised through the real close paths and the real exit handler.
// TestTheKillIsJudgedByWhatItObserved, below, covers the Windows half of it.

// survivingTerminalSession is a session whose teardown asked the platform to end
// it and outlived the asking. Its closeCh stays open, so Alive keeps answering
// truthfully: the process really is still there.
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
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		session:   session,
		serial:    serial,
		key:       fmt.Sprintf("erun-local-%d", serial),
	}
	app.sessions[managed.key] = managed
	return managed
}

// TestACloseThatCouldNotKillDoesNotOwnTheSurvivorsExit is the reported failure:
// the close reached the session, its Close came back reporting the process it
// was asked to end is still running, and the death that process eventually dies
// is its own. Attributing it to the close is the over-claim the operator sees
// as a close that succeeded being reported as something that went wrong.
func TestACloseThatCouldNotKillDoesNotOwnTheSurvivorsExit(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	managed := managedWithSession(app, newSurvivingTerminalSession(), 43)

	if _, err := app.CloseEnvironmentSessions(selection); !errors.Is(err, errKillDidNotLand) {
		t.Fatalf("a close that did not end its session must say so, got err=%v", err)
	}

	payload := exitPayloadFor(t, app, managed, "exit status 1")
	if payload.Deliberate {
		t.Fatal("a session the close could not kill must not have its later death reported as the close's own")
	}
}

// The same rule on the paths that do not go through the env walk: a single
// managed teardown, and the shutdown sweep that closes every session under the
// lock its callers already hold. Both mark the session closed and claim its
// exit, and neither may keep that claim for a session its Close left running.
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

// A session teardown that ends the process but cannot release the desktop's own
// end of it — a pty file, a handle — keeps the claim. That failure says nothing
// about the process, and this is the shape a POSIX teardown produces after a
// SIGKILL that landed: withdrawing on any Close error would report a close the
// operator watched work as a failure.
func TestACloseThatCouldNotReleaseItsPtyStillOwnsItsExit(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	selection := uiSelection{Tenant: "erun", Environment: "local"}
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

// TestTheKillIsJudgedByWhatItObserved pins the Windows half: what taskkill did
// and what the process did are two different facts, and only the second one
// decides. taskkill exits non-zero for a process that has already exited, so
// its status would report a survivor where there is none — and it can report
// success for a tree this session cannot see, so its status would equally miss
// one. The handle is the observation.
func TestTheKillIsJudgedByWhatItObserved(t *testing.T) {
	for _, test := range []struct {
		name       string
		killErr    error
		alive      bool
		wantMissed bool
	}{
		{
			// The "not found" case: a kill that needed no doing.
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
			// The status says it worked; the handle says the process is still
			// there. The handle is what the caller can act on.
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
