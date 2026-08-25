package main

import (
	"testing"
	"time"
)

// The AI repaint nudge shrinks the backend pty by a row and holds it. That is
// invisible when the pane is the blank reattached screen the nudge exists to
// redraw, and destructive when someone is typing into it: the TUI reflows the
// line being edited, and the submitted prompt came out holding several
// concatenated snapshots of it (#1330). These tests pin that a pane receiving
// input is left alone, and -- the control -- that a quiet pane still gets its
// repaint, so the guard cannot pass by disabling the feature.

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

// TestRepaintSessionSkipsPaneBeingTypedInto covers the tab-switch half of
// #1330: RepaintSession fires on EVERY switch into an AI pane, so switch-then-
// type is the reachable sequence and it must not resize the pty underneath it.
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

	// Give an errant nudge every chance to land before declaring it absent.
	if waitForResizes(t, session, 1, 300*time.Millisecond) {
		got, _ := session.lastResize()
		t.Fatalf("a pane being typed into must not be resized, got resize %v", got)
	}
}

// TestRepaintSessionNudgesQuietPane is the control: with no recent input the
// repaint must still happen, shrinking by one row and restoring. Without this,
// the guard above could pass by never nudging at all.
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

// TestRepaintSessionNudgesOrchestratorPane pins the real chain behind #1330's
// follow-up: an orchestrator pane runs the same main-screen TUI (claude) an AI
// tab does, but isAITabKind only recognizes sessionKindAI/sessionKindContributeAI,
// so RepaintSession's WINCH nudge was dead code for it -- the entire Go half of
// #1332 never ran for an orchestrator. This must fail against that code, and
// pass once RepaintSession gates on needsWINCHRepaint instead.
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

// TestMaybeNudgeAIRepaintSkipsPaneBeingTypedInto covers the attach-marker half.
// It also pins that the skip does NOT consume repaintNudged: this attach has
// not been repainted, so a later chunk must still be able to do it once the
// user stops typing.
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

// TestNudgeAIRepaintRestoresEarlyWhenInputArrives pins the third window: input
// that lands AFTER the shrink is already applied. The pty must not be held a
// row short for the rest of the settle -- it has to be restored promptly, since
// every millisecond it is wrong is a millisecond the TUI can reflow a line.
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
	// The point is not merely that it restores, but that it restores EARLY.
	// Waiting out the settle is the bug: every millisecond the pty is a row
	// short is a millisecond the TUI can reflow the line being typed.
	if elapsed := time.Since(started); elapsed >= aiRepaintNudgeSettle {
		t.Fatalf("input must cut the hold short, but it ran the full settle (%v >= %v)",
			elapsed, aiRepaintNudgeSettle)
	}
}
