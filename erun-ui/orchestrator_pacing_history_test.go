package main

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// Rearm zeroes the cap gauge; the cumulative history must survive it.
func TestOrchestratorPacingCumulativeHistorySurvivesAnswerRearm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := terminal.OrchestratorSessionKey("agent")
	setTestSession(app, key, &terminal.Session{PTY: session, Key: key, Serial: 5, Kind: terminal.KindOrchestrator})
	app.orchestrators["agent"] = &orchestratorSession{
		id: "agent", serial: 5, name: "agent",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()
	_, unlock := lockTerminalForTest(app)
	autoCount := app.orchestrators["agent"].pacingAutoNudgeCount
	lastAutoAt := app.orchestrators["agent"].pacingLastAutoNudgeAtUnix
	nudgeCount := app.orchestrators["agent"].pacingNudgeCount
	unlock()
	if autoCount != 1 || lastAutoAt == 0 {
		t.Fatalf("expected the nudge to be recorded in the cumulative history, got autoCount=%d lastAutoAt=%d", autoCount, lastAutoAt)
	}
	if nudgeCount != 1 {
		t.Fatalf("expected the cap's own gauge to move too, got %d", nudgeCount)
	}

	// A fresh report newer than the last nudge rearms on the next tick.
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: lastAutoAt + 10})
	app.reconcileOrchestratorPacing()

	_, unlock = lockTerminalForTest(app)
	nudgeCount = app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	autoCountAfter := app.orchestrators["agent"].pacingAutoNudgeCount
	lastAutoAtAfter := app.orchestrators["agent"].pacingLastAutoNudgeAtUnix
	unlock()
	if nudgeCount != 0 || capped {
		t.Fatalf("expected the answer to rearm the cap gauge, got nudgeCount=%d capped=%v", nudgeCount, capped)
	}
	if autoCountAfter != 1 || lastAutoAtAfter != lastAutoAt {
		t.Fatalf("expected the cumulative history to survive the rearm unchanged, got count=%d lastAt=%d (want count=1 lastAt=%d)",
			autoCountAfter, lastAutoAtAfter, lastAutoAt)
	}
}

func TestOrchestratorPacingNeverNudgedReportsNoHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := terminal.OrchestratorSessionKey("fresh")
	setTestSession(app, key, &terminal.Session{PTY: session, Key: key, Serial: 1, Kind: terminal.KindOrchestrator})
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 0 {
		t.Fatalf("a fresh session must not be nudged, got writes %v", session.Calls())
	}
	_, unlock := lockTerminalForTest(app)
	s := app.orchestrators["fresh"]
	unlock()
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

func TestOrchestratorPacingCappedHistorySurvivesRearm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	key := terminal.OrchestratorSessionKey("agent")
	setTestSession(app, key, &terminal.Session{PTY: session, Key: key, Serial: 5, Kind: terminal.KindOrchestrator})
	longAgo := time.Now().Add(-24 * time.Hour)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: longAgo}

	for i := 0; i < orchestratorPacingMaxNudges+1; i++ {
		app.reconcileOrchestratorPacing()
	}
	_, unlock := lockTerminalForTest(app)
	capped := app.orchestrators["agent"].pacingCapped
	lastCappedAt := app.orchestrators["agent"].pacingLastCappedAtUnix
	unlock()
	if !capped || lastCappedAt == 0 {
		t.Fatalf("expected the session to be capped with a recorded timestamp, got capped=%v lastCappedAt=%d", capped, lastCappedAt)
	}

	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: true, AtUnix: lastCappedAt + 10})
	app.reconcileOrchestratorPacing()

	_, unlock = lockTerminalForTest(app)
	capped = app.orchestrators["agent"].pacingCapped
	lastCappedAtAfter := app.orchestrators["agent"].pacingLastCappedAtUnix
	unlock()
	if capped {
		t.Fatal("expected the fresh busy report to clear the live capped gauge")
	}
	if lastCappedAtAfter != lastCappedAt {
		t.Fatalf("expected the capped history to survive the rearm unchanged, got %d (want %d)", lastCappedAtAfter, lastCappedAt)
	}
}
