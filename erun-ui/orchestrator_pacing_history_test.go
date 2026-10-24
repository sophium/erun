package main

import (
	"testing"
	"time"
)

// TestOrchestratorPacingCumulativeHistorySurvivesAnswerRearm pins the fix: a
// session that gets nudged and then answers must still report having been
// nudged, even though rearmOrchestratorPacing zeroes the cap's own
// pacingNudgeCount gauge back to 0. Before this, the hover card read
// pacingNudgeCount/pacingLastNudgeAtUnix directly, so a healthy session that
// answers every nudge collapsed back to "Not nudged" a moment after each one.
func TestOrchestratorPacingCumulativeHistorySurvivesAnswerRearm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 5, kind: sessionKindOrchestrator}
	app.orchestrators["agent"] = &orchestratorSession{
		id: "agent", serial: 5, name: "agent",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()
	app.mu.Lock()
	autoCount := app.orchestrators["agent"].pacingAutoNudgeCount
	lastAutoAt := app.orchestrators["agent"].pacingLastAutoNudgeAtUnix
	nudgeCount := app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if autoCount != 1 || lastAutoAt == 0 {
		t.Fatalf("expected the nudge to be recorded in the cumulative history, got autoCount=%d lastAutoAt=%d", autoCount, lastAutoAt)
	}
	if nudgeCount != 1 {
		t.Fatalf("expected the cap's own gauge to move too, got %d", nudgeCount)
	}

	// The session answers: a fresh activity report newer than the last nudge
	// rearms the cap gauge on the next tick.
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: lastAutoAt + 10})
	app.reconcileOrchestratorPacing()

	app.mu.Lock()
	nudgeCount = app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	autoCountAfter := app.orchestrators["agent"].pacingAutoNudgeCount
	lastAutoAtAfter := app.orchestrators["agent"].pacingLastAutoNudgeAtUnix
	app.mu.Unlock()
	if nudgeCount != 0 || capped {
		t.Fatalf("expected the answer to rearm the cap gauge, got nudgeCount=%d capped=%v", nudgeCount, capped)
	}
	if autoCountAfter != 1 || lastAutoAtAfter != lastAutoAt {
		t.Fatalf("expected the cumulative history to survive the rearm unchanged, got count=%d lastAt=%d (want count=1 lastAt=%d)",
			autoCountAfter, lastAutoAtAfter, lastAutoAt)
	}
}

// TestOrchestratorPacingNeverNudgedReportsNoHistory is the control for
// TestOrchestratorPacingCumulativeHistorySurvivesAnswerRearm: a session that
// has never been nudged or whipped must report zero history, so "never
// nudged" and "nudged, then answered" cannot collapse onto the same reading.
func TestOrchestratorPacingNeverNudgedReportsNoHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("fresh")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 0 {
		t.Fatalf("a fresh session must not be nudged, got writes %v", session.Calls())
	}
	app.mu.Lock()
	s := app.orchestrators["fresh"]
	app.mu.Unlock()
	if s.pacingAutoNudgeCount != 0 || s.pacingLastAutoNudgeAtUnix != 0 {
		t.Fatalf("expected no auto-nudge history, got count=%d lastAt=%d", s.pacingAutoNudgeCount, s.pacingLastAutoNudgeAtUnix)
	}
	if s.pacingWhipCount != 0 || s.pacingLastWhipAtUnix != 0 {
		t.Fatalf("expected no whip history, got count=%d lastAt=%d", s.pacingWhipCount, s.pacingLastWhipAtUnix)
	}
	if s.pacingLastCappedAtUnix != 0 {
		t.Fatalf("expected no capped history, got lastAt=%d", s.pacingLastCappedAtUnix)
	}
}

// TestOrchestratorPacingCappedHistorySurvivesRearm is the same
// survives-the-rearm fix applied to the cap notice itself:
// rearmOrchestratorPacing clears pacingCapped (the live "currently at the
// cap" gauge) the same way it clears pacingNudgeCount, but
// pacingLastCappedAtUnix must not follow it back to zero -- a session that
// resumed after hitting the cap is a different, more informative state than
// one that never came close.
func TestOrchestratorPacingCappedHistorySurvivesRearm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 5, kind: sessionKindOrchestrator}
	longAgo := time.Now().Add(-24 * time.Hour)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: longAgo}

	for i := 0; i < orchestratorPacingMaxNudges+1; i++ {
		app.reconcileOrchestratorPacing()
	}
	app.mu.Lock()
	capped := app.orchestrators["agent"].pacingCapped
	lastCappedAt := app.orchestrators["agent"].pacingLastCappedAtUnix
	app.mu.Unlock()
	if !capped || lastCappedAt == 0 {
		t.Fatalf("expected the session to be capped with a recorded timestamp, got capped=%v lastCappedAt=%d", capped, lastCappedAt)
	}

	// A fresh busy report rearms the live gauge.
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: true, AtUnix: lastCappedAt + 10})
	app.reconcileOrchestratorPacing()

	app.mu.Lock()
	capped = app.orchestrators["agent"].pacingCapped
	lastCappedAtAfter := app.orchestrators["agent"].pacingLastCappedAtUnix
	app.mu.Unlock()
	if capped {
		t.Fatal("expected the fresh busy report to clear the live capped gauge")
	}
	if lastCappedAtAfter != lastCappedAt {
		t.Fatalf("expected the capped history to survive the rearm unchanged, got %d (want %d)", lastCappedAtAfter, lastCappedAt)
	}
}
