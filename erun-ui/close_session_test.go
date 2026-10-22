package main

import (
	"sync"
	"testing"
	"time"
)

// TestADeliberatelyClosedSessionStillReportsItsExit is the reproduction for the
// silence the marked close introduced. CloseSession marks the session closed
// under a.mu before its PTY is touched, which is what keeps a session the
// operator just closed out of the stale-shell detector — the process is already
// reaped the moment the close lands, so an unmarked session is offered to the
// detector as "shell exited unexpectedly". That mark is not a claim that there
// is nothing to report, though: the close is still an exit, and the terminal-exit
// event, the registry deletion, and the ready release are what the frontend drops
// the tab on and what a gated action runner is blocked on.
//
// The interleaving is the point. The close lands from inside handleSessionOutput
// — reached through the emitter hook, which is where a re-entrant UI action or
// another emitter listener can close a session — so the reader comes back around
// to currentSessionFor and finds the mark rather than a read error. A reader
// parked in Read instead takes the read-error arm and finalizes there, which is
// why a test that starts no reader, or one that closes from outside the reader,
// never reaches the state this covers.
func TestADeliberatelyClosedSessionStillReportsItsExit(t *testing.T) {
	app := &App{
		sessions: make(map[string]*managedTerminal),
		busyEnvs: make(map[string]int),
	}
	session := newStubTerminalSession()
	managed := &managedTerminal{
		session:   session,
		key:       "command\x00erun\x00remote\x00close-during-output",
		serial:    71,
		kind:      sessionKindCommand,
		startedAt: time.Now(),
	}
	app.sessions[managed.key] = managed

	emits := newCapturedEmits()
	record := emits.fn()
	var closeOnce sync.Once
	app.SetEmitter(func(name string, args ...any) {
		record(name, args...)
		if name != terminalOutputEvent {
			return
		}
		closeOnce.Do(func() {
			if err := app.CloseSession(managed.serial); err != nil {
				t.Errorf("CloseSession: %v", err)
			}
		})
	})

	app.spawnStreamSession(managed)
	// The reader is the only finalizer and it returns once it has finished, so
	// this is the deterministic join on it rather than a wall-clock budget spent
	// guessing how long a goroutine needs.
	app.sessionWG.Wait()

	if got := len(emits.events(terminalExitEvent)); got != 1 {
		t.Fatalf("a deliberately closed session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("a deliberately closed session stayed registered in a.sessions")
	}
	managed.readyMu.Lock()
	ready := managed.readyClosed
	managed.readyMu.Unlock()
	if !ready {
		t.Fatalf("a deliberately closed session never released its ready gate")
	}
}

// TestASessionClosedBeforeItsFirstReadStillReportsItsExit is the neighbouring
// state of the same defect: the mark can also land before the reader's first
// currentSessionFor, so the reader's very first iteration is the one that finds
// it. Closing a directly-registered session and only then starting its reader is
// that ordering, held still instead of raced.
func TestASessionClosedBeforeItsFirstReadStillReportsItsExit(t *testing.T) {
	app := &App{
		sessions: make(map[string]*managedTerminal),
		busyEnvs: make(map[string]int),
	}
	session := newStubTerminalSession()
	managed := &managedTerminal{
		session:   session,
		key:       "command\x00erun\x00remote\x00closed-before-reader",
		serial:    72,
		kind:      sessionKindCommand,
		startedAt: time.Now(),
	}
	app.sessions[managed.key] = managed

	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	if err := app.CloseSession(managed.serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	app.spawnStreamSession(managed)
	app.sessionWG.Wait()

	if got := len(emits.events(terminalExitEvent)); got != 1 {
		t.Fatalf("a closed-before-its-reader session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("a closed-before-its-reader session stayed registered in a.sessions")
	}
}

// TestAnExitingSessionStillReportsItsExitExactlyOnce is the control for both
// cases above: the ordinary read-error arm, with nothing closed and no mark
// involved. The process ends, the reader sees the error, and the exit is
// finalized once — so a red above is about the deliberate-close mark, not about
// the exit path generally, and a duplication introduced by finalizing the mark
// would show up here as a second event.
func TestAnExitingSessionStillReportsItsExitExactlyOnce(t *testing.T) {
	app := &App{
		sessions: make(map[string]*managedTerminal),
		busyEnvs: make(map[string]int),
	}
	session := newStubTerminalSession()
	managed := &managedTerminal{
		session:   session,
		key:       "command\x00erun\x00remote\x00ordinary-exit",
		serial:    73,
		kind:      sessionKindCommand,
		startedAt: time.Now(),
	}
	app.sessions[managed.key] = managed

	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	app.spawnStreamSession(managed)
	// The process ends on its own: no close path runs, no mark is set.
	if err := session.Close(); err != nil {
		t.Fatalf("stub Close: %v", err)
	}
	app.sessionWG.Wait()

	if got := len(emits.events(terminalExitEvent)); got != 1 {
		t.Fatalf("an exiting session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("an exited session stayed registered in a.sessions")
	}
}
