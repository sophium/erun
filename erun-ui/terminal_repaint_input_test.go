package main

import (
	"testing"
	"time"
)

// The repaint nudge must leave a pane being typed into alone, yet still
// repaint a quiet one.

func shortenRepaintTimings(t *testing.T) {
	t.Helper()
	delay, settle := aiRepaintNudgeDelay, aiRepaintNudgeSettle
	aiRepaintNudgeDelay = 5 * time.Millisecond
	aiRepaintNudgeSettle = 400 * time.Millisecond
	t.Cleanup(func() {
		aiRepaintNudgeDelay, aiRepaintNudgeSettle = delay, settle
	})
}

func (s *stubTerminalSession) resizeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resizes)
}

func (s *stubTerminalSession) lastResize() ([2]int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resizes) == 0 {
		return [2]int{}, false
	}
	return s.resizes[len(s.resizes)-1], true
}

func newAITerminalForRepaint(app *App, serial int) (*managedTerminal, *stubTerminalSession) {
	session := newSilentStubTerminalSession()
	key := "ai-pane"
	managed := &managedTerminal{
		session:  session,
		key:      key,
		serial:   serial,
		kind:     sessionKindAI,
		lastCols: 120,
		lastRows: 40,
	}
	app.nextSerial = serial
	app.sessions[key] = managed
	return managed, session
}

// waitForResizes polls until the session has recorded at least n resizes, so an
// asynchronous nudge does not race the assertion.
func waitForResizes(t *testing.T, session *stubTerminalSession, n int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if session.resizeCount() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRepaintSessionSkipsPaneBeingTypedInto(t *testing.T) {
	shortenRepaintTimings(t)
	app := NewApp(erunUIDeps{})
	_, session := newAITerminalForRepaint(app, 1)

	if err := app.SendSessionInput(1, "spreadsheet"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}
	if err := app.RepaintSession(1); err != nil {
		t.Fatalf("RepaintSession failed: %v", err)
	}

	if waitForResizes(t, session, 1, 300*time.Millisecond) {
		got, _ := session.lastResize()
		t.Fatalf("a pane being typed into must not be resized, got resize %v", got)
	}
}

func TestRepaintSessionNudgesQuietPane(t *testing.T) {
	shortenRepaintTimings(t)
	app := NewApp(erunUIDeps{})
	_, session := newAITerminalForRepaint(app, 1)

	if err := app.RepaintSession(1); err != nil {
		t.Fatalf("RepaintSession failed: %v", err)
	}
	if !waitForResizes(t, session, 2, 3*time.Second) {
		t.Fatalf("a quiet pane must still be nudged, got %d resizes", session.resizeCount())
	}
	if got, _ := session.lastResize(); got != [2]int{120, 40} {
		t.Fatalf("the nudge must restore the pty to its real size, ended at %v", got)
	}
}

func newOrchestratorTerminalForRepaint(app *App, serial int) (*managedTerminal, *stubTerminalSession) {
	session := newSilentStubTerminalSession()
	key := "orchestrator-pane"
	managed := &managedTerminal{
		session:  session,
		key:      key,
		serial:   serial,
		kind:     sessionKindOrchestrator,
		lastCols: 120,
		lastRows: 40,
	}
	app.nextSerial = serial
	app.sessions[key] = managed
	return managed, session
}

// isAITabKind excludes orchestrators, so RepaintSession must gate on
// needsWINCHRepaint.
func TestRepaintSessionNudgesOrchestratorPane(t *testing.T) {
	shortenRepaintTimings(t)
	app := NewApp(erunUIDeps{})
	_, session := newOrchestratorTerminalForRepaint(app, 1)

	if err := app.RepaintSession(1); err != nil {
		t.Fatalf("RepaintSession failed: %v", err)
	}
	if !waitForResizes(t, session, 2, 3*time.Second) {
		t.Fatalf("an orchestrator pane must be nudged just like an AI tab, got %d resizes", session.resizeCount())
	}
	if got, _ := session.lastResize(); got != [2]int{120, 40} {
		t.Fatalf("the nudge must restore the pty to its real size, ended at %v", got)
	}
}

// Skipping must not consume repaintNudged.
func TestMaybeNudgeAIRepaintSkipsPaneBeingTypedInto(t *testing.T) {
	shortenRepaintTimings(t)
	app := NewApp(erunUIDeps{})
	managed, session := newAITerminalForRepaint(app, 1)

	if err := app.SendSessionInput(1, "hello"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}
	app.maybeNudgeAIRepaint(managed, append([]byte("prefix"), aiAttachMarker...))

	if waitForResizes(t, session, 1, 300*time.Millisecond) {
		t.Fatalf("the attach nudge must not resize a pane being typed into")
	}
	app.mu.Lock()
	nudged := managed.repaintNudged
	app.mu.Unlock()
	if nudged {
		t.Fatal("skipping for input must not consume the once-per-attach nudge")
	}
}

// Input after the shrink must restore the pty early, not after the settle.
func TestNudgeAIRepaintRestoresEarlyWhenInputArrives(t *testing.T) {
	shortenRepaintTimings(t)
	app := NewApp(erunUIDeps{})
	managed, session := newAITerminalForRepaint(app, 1)

	gen := app.sessionInputGen(managed)
	done := make(chan struct{})
	started := time.Now()
	go func() {
		app.nudgeAIRepaint(managed, 120, 40, 0, gen)
		close(done)
	}()

	// Wait for the shrink, then type into it mid-hold.
	if !waitForResizes(t, session, 1, 2*time.Second) {
		t.Fatal("the nudge never applied its shrink")
	}
	if got, _ := session.lastResize(); got != [2]int{120, 39} {
		t.Fatalf("expected a one-row shrink, got %v", got)
	}
	if err := app.SendSessionInput(1, "x"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the nudge did not finish")
	}
	if got, _ := session.lastResize(); got != [2]int{120, 40} {
		t.Fatalf("the pty must always end restored, ended at %v", got)
	}
	if elapsed := time.Since(started); elapsed >= aiRepaintNudgeSettle {
		t.Fatalf("input must cut the hold short, but it ran the full settle (%v >= %v)",
			elapsed, aiRepaintNudgeSettle)
	}
}
