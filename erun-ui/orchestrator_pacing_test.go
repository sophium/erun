package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// callRecordingSession wraps a stub session but also records each Write call
// separately, so a test can assert the pacing text and its submitting CR
// arrived as two distinct writes rather than one combined one — a combined
// write risks the harness reading the CR as part of the pasted text.
type callRecordingSession struct {
	*stubTerminalSession
	mu    sync.Mutex
	calls []string
}

func newCallRecordingSession() *callRecordingSession {
	return &callRecordingSession{stubTerminalSession: newStubTerminalSession()}
}

func (s *callRecordingSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.calls = append(s.calls, string(p))
	s.mu.Unlock()
	return s.stubTerminalSession.Write(p)
}

func (s *callRecordingSession) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func TestDecideOrchestratorPacing(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Minute)
	stale := now.Add(-orchestratorPacingStaleAfter - time.Minute)

	cases := []struct {
		name string
		c    orchestratorPacingCandidate
		want orchestratorPacingDecision
	}{
		{"fresh activity is never nudged", orchestratorPacingCandidate{alive: true, lastActiveAt: fresh}, orchestratorPacingNone},
		{"stale and alive is nudged", orchestratorPacingCandidate{alive: true, lastActiveAt: stale}, orchestratorPacingNudge},
		{"not alive (session gone) is never nudged", orchestratorPacingCandidate{alive: false, lastActiveAt: stale}, orchestratorPacingNone},
		{"a running background shell suppresses the nudge", orchestratorPacingCandidate{alive: true, shellRunning: true, lastActiveAt: stale}, orchestratorPacingNone},
		{"already capped stays silent", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, capped: true}, orchestratorPacingNone},
		{"crossing the max count caps instead of nudging", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, nudgeCount: orchestratorPacingMaxNudges}, orchestratorPacingCap},
		{"below the max count still nudges", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, nudgeCount: orchestratorPacingMaxNudges - 1}, orchestratorPacingNudge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideOrchestratorPacing(tc.c, now); got != tc.want {
				t.Fatalf("decideOrchestratorPacing(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}

// TestReconcileOrchestratorPacingNudgesAQuietSession is the read-decide-write
// path end to end: a session whose activity report has not moved in over ten
// minutes gets the pacing text, then a separate carriage return to submit it.
func TestReconcileOrchestratorPacingNudgesAQuietSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orig := orchestratorPacingNudgeSettle
	orchestratorPacingNudgeSettle = 0
	defer func() { orchestratorPacingNudgeSettle = orig }()

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	managed := &managedTerminal{session: session, key: key, serial: 5, kind: sessionKindOrchestrator}
	app.sessions[key] = managed
	app.orchestrators["agent"] = &orchestratorSession{
		id:        "agent",
		serial:    5,
		name:      "agent",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()

	calls := session.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected two separate writes (text, then CR), got %v", calls)
	}
	if calls[0] != orchestratorPacingNudgeText {
		t.Fatalf("first write = %q, want the pacing text", calls[0])
	}
	if calls[1] != "\r" {
		t.Fatalf("second write = %q, want a bare carriage return", calls[1])
	}

	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if count != 1 {
		t.Fatalf("expected the nudge count to be recorded, got %d", count)
	}
}

// TestReconcileOrchestratorPacingSkipsFreshBackgroundShellAndTransient locks
// the three bounds a nudge must never cross: a session with fresh activity, one
// running a background shell, and a transient (Investigate) session, none of
// which have any pacing state to nudge from.
func TestReconcileOrchestratorPacingSkipsFreshBackgroundShellAndTransient(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	freshSession := newCallRecordingSession()
	freshKey := orchestratorSessionKey("fresh")
	app.sessions[freshKey] = &managedTerminal{session: freshSession, key: freshKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, startedAt: time.Now()}

	shellSession := newCallRecordingSession()
	shellKey := orchestratorSessionKey("shell")
	app.sessions[shellKey] = &managedTerminal{session: shellSession, key: shellKey, serial: 2, kind: sessionKindOrchestrator}
	app.orchestrators["shell"] = &orchestratorSession{
		id: "shell", serial: 2,
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		shellRunning: true,
	}

	transientSession := newCallRecordingSession()
	transientKey := orchestratorSessionKey("")
	app.sessions[transientKey] = &managedTerminal{session: transientSession, key: transientKey, serial: 3, kind: sessionKindOrchestrator}
	app.orchestrators[""] = &orchestratorSession{
		serial: 3, transient: true,
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()

	for name, s := range map[string]*callRecordingSession{"fresh": freshSession, "shell": shellSession, "transient": transientSession} {
		if len(s.Calls()) != 0 {
			t.Fatalf("%s session must not be nudged, got writes %v", name, s.Calls())
		}
	}
}

// TestOrchestratorPacingCapsAfterRepeatedSilenceAndRearmsOnFreshBusy pins the
// bound: after the max un-answered nudges, erun stops nudging and posts a
// notice exactly once, and a fresh busy report re-arms it so nudging resumes
// against a session that later goes quiet again.
func TestOrchestratorPacingCapsAfterRepeatedSilenceAndRearmsOnFreshBusy(t *testing.T) {
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

	for i := 0; i < orchestratorPacingMaxNudges; i++ {
		app.reconcileOrchestratorPacing()
	}
	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	app.mu.Unlock()
	if count != orchestratorPacingMaxNudges || capped {
		t.Fatalf("expected %d nudges and no cap yet, got count=%d capped=%v", orchestratorPacingMaxNudges, count, capped)
	}
	writesBeforeCap := len(session.Calls())

	// One more stale tick crosses the cap: no further write, one notice.
	app.reconcileOrchestratorPacing()
	app.mu.Lock()
	capped = app.orchestrators["agent"].pacingCapped
	app.mu.Unlock()
	if !capped {
		t.Fatal("expected the cap to latch after the max nudges")
	}
	if len(session.Calls()) != writesBeforeCap {
		t.Fatalf("a capped orchestrator must not be written to again, got %d new writes", len(session.Calls())-writesBeforeCap)
	}
	notices := emits.events(appNotificationEvent)
	if len(notices) != 1 {
		t.Fatalf("expected exactly one capped notification, got %d", len(notices))
	}

	// Another stale tick while still capped must not repeat the notice.
	app.reconcileOrchestratorPacing()
	if len(emits.events(appNotificationEvent)) != 1 {
		t.Fatal("the capped notice must not repeat every tick")
	}

	// A fresh busy report rearms it. AtUnix is derived from the last recorded
	// nudge rather than time.Now(): both are whole-second unix timestamps, and
	// a real-clock comparison here would be flaky whenever the test runs fast
	// enough to land in the same second as the last nudge.
	app.mu.Lock()
	lastNudgeAtUnix := app.orchestrators["agent"].pacingLastNudgeAtUnix
	app.mu.Unlock()
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: true, AtUnix: lastNudgeAtUnix + 10})
	app.reconcileOrchestratorPacing()
	app.mu.Lock()
	capped = app.orchestrators["agent"].pacingCapped
	count = app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if capped || count != 0 {
		t.Fatalf("a fresh busy report must rearm the cap, got capped=%v count=%d", capped, count)
	}
}

// TestSendSessionInputRearmsOrchestratorPacing locks the other rearm path: real
// operator input into the pane clears the cap and the count immediately,
// without waiting for a fresh busy report.
func TestSendSessionInputRearmsOrchestratorPacing(t *testing.T) {
	app := NewApp(erunUIDeps{})
	session := newStubTerminalSession()
	key := orchestratorSessionKey("agent")
	app.nextSerial = 1
	managed := &managedTerminal{session: session, key: key, serial: 1, kind: sessionKindOrchestrator}
	app.sessions[key] = managed
	app.orchestrators["agent"] = &orchestratorSession{
		id: "agent", serial: 1,
		pacingNudgeCount: orchestratorPacingMaxNudges,
		pacingCapped:     true,
	}

	if err := app.SendSessionInput(1, "hello"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}

	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	app.mu.Unlock()
	if count != 0 || capped {
		t.Fatalf("expected real operator input to rearm pacing, got count=%d capped=%v", count, capped)
	}
	if !strings.Contains(session.WrittenString(), "hello") {
		t.Fatal("expected the input to still reach the session")
	}
}
