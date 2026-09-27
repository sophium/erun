package main

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// newStaleShellTestApp is the least an App needs for the stale-shell reconciler:
// a session registry to scan and an activity queue to report into.
func newStaleShellTestApp() *App {
	app := &App{sessions: make(map[string]*managedTerminal)}
	app.activityQueue = newActivityQueueStore(nil, nil)
	return app
}

// addStaleShellSession registers a session the detector is willing to ask
// about — it has a pid — with a caller-chosen start time, so the freshly-started
// grace window is a parameter rather than a sleep. No reader goroutine runs for
// it: the state under test is what a close records, and building it directly is
// what keeps the assertions deterministic instead of won against the reader.
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

// reapedShellSession is a terminalSession whose process is already gone while
// its PTY is still open and the session is still registered — the state the
// detector exists to surface, and the state the drawer offers Kill against. A
// shell reaches it by being signalled, OOM-killed, or losing its exec stream
// without any desktop close path claiming the exit. The shared stub's only lever
// for a dead process is Close, which cannot express "dead before anything closed
// it", so the probe's answer is pinned directly here.
type reapedShellSession struct {
	*stubTerminalSession
	mu    sync.Mutex
	alive bool
}

// newReapedShellSession starts already dead: the reaping happened before any
// close path ran, which is what makes the session stale.
func newReapedShellSession() *reapedShellSession {
	return &reapedShellSession{stubTerminalSession: newSilentStubTerminalSession()}
}

func (s *reapedShellSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// Close tears the PTY down without resurrecting the process: a close on a
// session whose process is already reaped does not change the probe's answer.
func (s *reapedShellSession) Close() error {
	return s.stubTerminalSession.Close()
}

// addReapedShellSession registers a session whose process is already gone, with
// a caller-chosen start time so the freshly-started grace window is a parameter
// rather than a sleep.
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

// aStaleShellSessionAge is comfortably past staleShellDebounce, so a session
// started this long ago is one the detector will ask about.
func aStaleShellSessionAge() time.Time { return time.Now().Add(-2 * staleShellDebounce) }

// TestADeliberatelyClosedSessionIsNotReportedAsAStaleShell is the reproduction
// for the close-path gap the reaped-process probe fix exposed. Now that the
// session is asked whether its process is live (activity_stale_sessions.go:80),
// a session the desktop ended itself is indistinguishable from one that died on
// its own unless the close says which it was: CloseSession closed the PTY
// without marking the session closed, so a deliberately closed, killed-and-reaped
// session was offered to the detector and surfaced as "shell exited
// unexpectedly" until its reader got around to finalizing the exit.
func TestADeliberatelyClosedSessionIsNotReportedAsAStaleShell(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addStaleShellSession(app, 41, aStaleShellSessionAge())

	if err := app.CloseSession(managed.serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	// Precondition, so a close that quietly did nothing cannot pass this test:
	// the process really is gone, which is the only reason the detector would
	// otherwise ask about it.
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

// TestASessionWhoseProcessDiedOnItsOwnIsStillReportedAsAStaleShell is the
// control for the test above: it holds the detector, not the close, still. The
// cue is right to ask — the process is gone and nothing claimed the exit — so
// the same dead session, reached without any close path, must still be reported
// and queued.
func TestASessionWhoseProcessDiedOnItsOwnIsStillReportedAsAStaleShell(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addStaleShellSession(app, 42, aStaleShellSessionAge())

	// The stub's only lever for a dead process is the one Close pulls, reached
	// directly here so no deliberate-close path gets to claim the exit.
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

// TestAFreshlyStartedSessionIsHeldOutOfTheStaleDetector is the neighbouring-state
// control: the same deliberate close, inside the freshly-started grace window,
// must not be reported either — a shell that forked before its parent exited is
// not a stale shell.
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

// TestKillingAStaleSessionDoesNotQueueItAgain covers the detector's own button.
// KillSession is what the drawer puts in front of the operator for a shell the
// detector reports as dead, and it closed that PTY without marking the session
// closed or removing it from the registry — the same unmarked close the tab
// close was fixed for. By the time the button lands the process is already
// reaped, so the next detector pass found the same registered session, asked the
// probe, read "not alive", and re-queued the entry the button had just
// dismissed: the row the operator killed came straight back.
func TestKillingAStaleSessionDoesNotQueueItAgain(t *testing.T) {
	app := newStaleShellTestApp()
	managed, session := addReapedShellSession(app, 51, aStaleShellSessionAge())

	// The entry the Kill button belongs to, produced the way production produces
	// it: one detector pass over the reaped shell.
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

	// The next pass is the reproduction: the session the operator just killed is
	// reaped and still registered, which is precisely what the detector reports
	// on, so without the close claiming it the row comes straight back.
	app.reconcileStaleShellsOnce()
	if entry, ok := app.activityQueue.findByID(staleShellActivityID(managed.serial)); ok {
		t.Fatalf("a session the operator killed was re-queued as stale: %q", entry.Error)
	}
}

// TestKillSessionForAnUnknownSerialLeavesTheDetectorAlone is the control for the
// test above: the mark the fix adds must be what suppresses the report, and a
// Kill that finds no session must claim nothing. The dead shell stays reported
// through a Kill aimed at another serial, so a dead shell nothing has claimed is
// still surfaced.
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
