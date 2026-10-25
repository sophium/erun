package main

import (
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The closed mark keeps the session out of the stale-shell detector but must not
// suppress the exit event. Closing from inside handleSessionOutput makes the reader
// find the mark rather than a read error.
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
		if name != uimodel.TerminalOutputEvent {
			return
		}
		closeOnce.Do(func() {
			if err := app.CloseSession(managed.serial); err != nil {
				t.Errorf("CloseSession: %v", err)
			}
		})
	})

	app.spawnStreamSession(managed)
	// The reader is the only finalizer, so this joins on it deterministically.
	app.sessionWG.Wait()

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
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

// The mark lands before the reader's first currentSessionFor.
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

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
		t.Fatalf("a closed-before-its-reader session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("a closed-before-its-reader session stayed registered in a.sessions")
	}
}

// Control: the ordinary read-error arm finalizes exactly once.
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
	if err := session.Close(); err != nil {
		t.Fatalf("stub Close: %v", err)
	}
	app.sessionWG.Wait()

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
		t.Fatalf("an exiting session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("an exited session stayed registered in a.sessions")
	}
}
