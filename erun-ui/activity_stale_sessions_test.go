package main

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStaleShellTestApp() *App {
	app := &App{sessions: make(map[string]*managedTerminal)}
	app.activityQueue = newActivityQueueStore(nil, nil)
	return app
}

// addStaleShellSession runs no reader goroutine so the closed state stays deterministic.
func addStaleShellSession(app *App, serial int, startedAt time.Time) (*managedTerminal, *stubTerminalSession) {
	session := newStubTerminalSession()
	session.pid = serial
	managed := &managedTerminal{
		session:   session,
		key:       "extra\x00" + strconv.Itoa(serial),
		serial:    serial,
		kind:      sessionKindCommand,
		startedAt: startedAt,
	}
	app.mu.Lock()
	app.sessions[managed.key] = managed
	app.mu.Unlock()
	return managed, session
}

// reapedShellSession's process is gone while its PTY is still open and registered.
type reapedShellSession struct {
	*stubTerminalSession
	mu    sync.Mutex
	alive bool
}

func newReapedShellSession() *reapedShellSession {
	return &reapedShellSession{stubTerminalSession: newSilentStubTerminalSession()}
}

func (s *reapedShellSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// Close does not resurrect the process.
func (s *reapedShellSession) Close() error {
	return s.stubTerminalSession.Close()
}

func addReapedShellSession(app *App, serial int, startedAt time.Time) (*managedTerminal, *reapedShellSession) {
	session := newReapedShellSession()
	session.pid = serial
	managed := &managedTerminal{
		session:   session,
		key:       "extra\x00" + strconv.Itoa(serial),
		serial:    serial,
		kind:      sessionKindCommand,
		startedAt: startedAt,
	}
	app.mu.Lock()
	app.sessions[managed.key] = managed
	app.mu.Unlock()
	return managed, session
}

func aStaleShellSessionAge() time.Time { return time.Now().Add(-2 * staleShellDebounce) }

// A session the desktop closed itself must not look like one that died on its own.
func TestADeliberatelyClosedSessionIsNotReportedAsAStaleShell(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addStaleShellSession(app, 41, aStaleShellSessionAge())

	if err := app.CloseSession(managed.serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	// Precondition: the process really is gone.
	if session.Alive() {
		t.Fatalf("CloseSession left the session's process alive")
	}

	if snapshots := app.collectStaleSessionSnapshots(); len(snapshots) != 0 {
		t.Fatalf("a session the operator just closed was offered to the stale detector: %+v", snapshots)
	}
	app.reconcileStaleShellsOnce()
	if entry, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); ok {
		t.Fatalf("a deliberate close queued a stale-shell entry: %q", entry.Error)
	}
}

func TestASessionWhoseProcessDiedOnItsOwnIsStillReportedAsAStaleShell(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addStaleShellSession(app, 42, aStaleShellSessionAge())

	// Kill the process without any deliberate-close path claiming the exit.
	if err := session.Close(); err != nil {
		t.Fatalf("stub Close: %v", err)
	}

	snapshots := app.collectStaleSessionSnapshots()
	if len(snapshots) != 1 || snapshots[0].serial != managed.serial {
		t.Fatalf("a session that died on its own must still be reported stale, got %+v", snapshots)
	}
	app.reconcileStaleShellsOnce()
	entry, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial))
	if !ok {
		t.Fatalf("expected a stale-shell queue entry for serial %d", managed.serial)
	}
	if !strings.Contains(entry.Error, "shell exited unexpectedly") {
		t.Fatalf("unexpected stale-shell entry error: %q", entry.Error)
	}
}

// A shell that forked before its parent exited is not stale.
func TestAFreshlyStartedSessionIsHeldOutOfTheStaleDetector(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addStaleShellSession(app, 43, time.Now())

	if err := app.CloseSession(managed.serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if session.Alive() {
		t.Fatalf("CloseSession left the session's process alive")
	}

	if snapshots := app.collectStaleSessionSnapshots(); len(snapshots) != 0 {
		t.Fatalf("a freshly started session was reported stale: %+v", snapshots)
	}
}

// Kill must mark the session closed so the next detector pass does not re-queue it.
func TestKillingAStaleSessionDoesNotQueueItAgain(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addReapedShellSession(app, 51, aStaleShellSessionAge())

	app.reconcileStaleShellsOnce()
	if _, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); !ok {
		t.Fatalf("precondition: the detector must report the reaped shell before Kill is offered")
	}

	if !app.KillSession(managed.serial) {
		t.Fatalf("KillSession(%d) found no session to kill", managed.serial)
	}
	if session.Alive() {
		t.Fatalf("the session read alive after KillSession")
	}
	if _, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); ok {
		t.Fatalf("KillSession did not dismiss the entry it was pressed on")
	}

	app.reconcileStaleShellsOnce()
	if entry, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); ok {
		t.Fatalf("a session the operator killed was re-queued as stale: %q", entry.Error)
	}
}

// A Kill aimed at another serial must not suppress the report.
func TestKillSessionForAnUnknownSerialLeavesTheDetectorAlone(t *testing.T) {
	app := newStaleShellTestApp()
	managed, _ := addReapedShellSession(app, 52, aStaleShellSessionAge())

	app.reconcileStaleShellsOnce()
	if _, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); !ok {
		t.Fatalf("precondition: the detector must report the dead shell")
	}
	if app.KillSession(9999) {
		t.Fatalf("KillSession reported a session for a serial it holds none for")
	}

	app.reconcileStaleShellsOnce()
	if _, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); !ok {
		t.Fatalf("a dead shell no Kill landed on must stay reported as stale")
	}
}
