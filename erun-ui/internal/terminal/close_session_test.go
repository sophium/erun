package terminal

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
	app := newTestService(Deps{})
	session := newStubTerminalSession()
	managed := &Session{
		PTY:       session,
		Key:       "command\x00erun\x00remote\x00close-during-output",
		Serial:    71,
		Kind:      KindCommand,
		StartedAt: time.Now(),
	}
	app.sessions[managed.Key] = managed

	emits := newCapturedEmits()
	record := emits.fn()
	var closeOnce sync.Once
	app.d.Emit = func(name string, args ...any) {
		record(name, args...)
		if name != uimodel.TerminalOutputEvent {
			return
		}
		closeOnce.Do(func() {
			if err := app.CloseSession(managed.Serial); err != nil {
				t.Errorf("CloseSession: %v", err)
			}
		})
	}

	app.SpawnStream(managed)
	// The reader is the only finalizer, so this joins on it deterministically.
	app.sessionWG.Wait()

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
		t.Fatalf("a deliberately closed session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.Key]
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
	app := newTestService(Deps{})
	session := newStubTerminalSession()
	managed := &Session{
		PTY:       session,
		Key:       "command\x00erun\x00remote\x00closed-before-reader",
		Serial:    72,
		Kind:      KindCommand,
		StartedAt: time.Now(),
	}
	app.sessions[managed.Key] = managed

	emits := newCapturedEmits()
	app.d.Emit = emits.fn()

	if err := app.CloseSession(managed.Serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	app.SpawnStream(managed)
	app.sessionWG.Wait()

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
		t.Fatalf("a closed-before-its-reader session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.Key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("a closed-before-its-reader session stayed registered in a.sessions")
	}
}

// Control: the ordinary read-error arm finalizes exactly once.
func TestAnExitingSessionStillReportsItsExitExactlyOnce(t *testing.T) {
	app := newTestService(Deps{})
	session := newStubTerminalSession()
	managed := &Session{
		PTY:       session,
		Key:       "command\x00erun\x00remote\x00ordinary-exit",
		Serial:    73,
		Kind:      KindCommand,
		StartedAt: time.Now(),
	}
	app.sessions[managed.Key] = managed

	emits := newCapturedEmits()
	app.d.Emit = emits.fn()

	app.SpawnStream(managed)
	if err := session.Close(); err != nil {
		t.Fatalf("stub Close: %v", err)
	}
	app.sessionWG.Wait()

	if got := len(emits.events(uimodel.TerminalExitEvent)); got != 1 {
		t.Fatalf("an exiting session emitted %d terminal-exit events, want exactly 1", got)
	}
	app.mu.Lock()
	_, stillRegistered := app.sessions[managed.Key]
	app.mu.Unlock()
	if stillRegistered {
		t.Fatalf("an exited session stayed registered in a.sessions")
	}
}
