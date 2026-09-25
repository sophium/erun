package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// orchestratorPacingMarkerTexts decodes every terminalOutputEvent payload the
// captured emitter saw into its raw string, in emission order, so a test can
// assert on the exact marker text a nudge rendered.
func orchestratorPacingMarkerTexts(emits *capturedEmits) []string {
	var texts []string
	for _, evt := range emits.events(terminalOutputEvent) {
		payload, ok := evt.(terminalOutputPayload)
		if !ok {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(payload.Data)
		if err != nil {
			continue
		}
		texts = append(texts, string(data))
	}
	return texts
}

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
		name       string
		c          orchestratorPacingCandidate
		want       orchestratorPacingDecision
		wantReason orchestratorPacingReason
	}{
		{"fresh activity is never nudged", orchestratorPacingCandidate{alive: true, lastActiveAt: fresh}, orchestratorPacingNone, orchestratorPacingReasonFresh},
		{"stale and alive is nudged", orchestratorPacingCandidate{alive: true, lastActiveAt: stale}, orchestratorPacingNudge, orchestratorPacingReasonNudge},
		{"not alive (session gone) is never nudged", orchestratorPacingCandidate{alive: false, lastActiveAt: stale}, orchestratorPacingNone, orchestratorPacingReasonNotAlive},
		{
			"a stale session nudges even with a background shell recorded running: pacing no longer reads that fact",
			orchestratorPacingCandidate{alive: true, lastActiveAt: stale},
			orchestratorPacingNudge, orchestratorPacingReasonNudge,
		},
		{"already capped stays silent", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, capped: true}, orchestratorPacingNone, orchestratorPacingReasonAlreadyCapped},
		{"crossing the max count caps instead of nudging", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, nudgeCount: orchestratorPacingMaxNudges}, orchestratorPacingCap, orchestratorPacingReasonCapCrossed},
		{"below the max count still nudges", orchestratorPacingCandidate{alive: true, lastActiveAt: stale, nudgeCount: orchestratorPacingMaxNudges - 1}, orchestratorPacingNudge, orchestratorPacingReasonNudge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDecision, gotReason := decideOrchestratorPacing(tc.c, now)
			if gotDecision != tc.want {
				t.Fatalf("decideOrchestratorPacing(%+v) decision = %v, want %v", tc.c, gotDecision, tc.want)
			}
			if gotReason != tc.wantReason {
				t.Fatalf("decideOrchestratorPacing(%+v) reason = %v, want %v", tc.c, gotReason, tc.wantReason)
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

// TestReconcileOrchestratorPacingSkipsFreshAndTransient locks the two bounds a
// nudge must never cross: a session with fresh activity, and a transient
// (Investigate) session, neither of which has any pacing state to nudge from.
func TestReconcileOrchestratorPacingSkipsFreshAndTransient(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	freshSession := newCallRecordingSession()
	freshKey := orchestratorSessionKey("fresh")
	app.sessions[freshKey] = &managedTerminal{session: freshSession, key: freshKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, startedAt: time.Now()}

	transientSession := newCallRecordingSession()
	transientKey := orchestratorSessionKey("")
	app.sessions[transientKey] = &managedTerminal{session: transientSession, key: transientKey, serial: 3, kind: sessionKindOrchestrator}
	app.orchestrators[""] = &orchestratorSession{
		serial: 3, transient: true,
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()

	for name, s := range map[string]*callRecordingSession{"fresh": freshSession, "transient": transientSession} {
		if len(s.Calls()) != 0 {
			t.Fatalf("%s session must not be nudged, got writes %v", name, s.Calls())
		}
	}
}

// TestReconcileOrchestratorPacingNudgesThroughABackgroundShell is the
// regression test for erun#1376: a stale orchestrator with a background shell
// recorded running must still be nudged. Before the fix, `shellRunning` on the
// candidate suppressed the nudge outright — exactly the case a long-running
// build left going while its own turn had died mid-response, the scenario the
// issue traced from a 20-minute silent pane.
func TestReconcileOrchestratorPacingNudgesThroughABackgroundShell(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("shell")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 2, kind: sessionKindOrchestrator}
	app.orchestrators["shell"] = &orchestratorSession{
		id: "shell", serial: 2,
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		shellRunning: true,
	}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected a background shell to no longer suppress the nudge, got writes %v", session.Calls())
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

// TestOrchestratorPacingRearmsOnIdleReplyAcrossManyStaleCycles pins the bug: a
// session that answers every pacing nudge with a short reply — one that ends
// back at idle before the next reconciler tick samples it — must never accrue
// toward the cap. Driving reconcileOrchestratorPacingOne directly with a
// synthetic, always-advancing clock lets the test push the session through
// many more stale-then-reply cycles than orchestratorPacingMaxNudges without
// waiting on the real 10-minute staleness window between them.
func TestOrchestratorPacingRearmsOnIdleReplyAcrossManyStaleCycles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 5, kind: sessionKindOrchestrator}
	base := time.Unix(1_700_000_000, 0)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: base}

	cycles := orchestratorPacingMaxNudges + 2
	tick := base
	for i := 0; i < cycles; i++ {
		// Advance well past the staleness window so this cycle's first tick
		// is due a nudge, exactly as an orchestrator that replies and then
		// falls quiet again for the next stretch would look.
		tick = tick.Add(orchestratorPacingStaleAfter + time.Minute)

		rows := app.orchestratorPacingRows()
		if len(rows) != 1 {
			t.Fatalf("expected one pacing row, got %d", len(rows))
		}
		decision, _ := app.reconcileOrchestratorPacingOne(rows[0], tick, false)
		if decision != orchestratorPacingNudge {
			t.Fatalf("cycle %d: expected a nudge, got decision=%v", i, decision)
		}

		app.mu.Lock()
		lastNudgeAtUnix := app.orchestrators["agent"].pacingLastNudgeAtUnix
		app.mu.Unlock()

		// The session answers in one line and returns to idle well before
		// the next tick — the compliant case the bug missed.
		replyAt := time.Unix(lastNudgeAtUnix, 0).Add(time.Second)
		writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: replyAt.Unix()})

		// The next tick, shortly after the reply, must observe the fresh
		// report and rearm rather than letting the count carry forward.
		tick = replyAt.Add(10 * time.Second)
		rows = app.orchestratorPacingRows()
		app.reconcileOrchestratorPacingOne(rows[0], tick, false)

		app.mu.Lock()
		count := app.orchestrators["agent"].pacingNudgeCount
		capped := app.orchestrators["agent"].pacingCapped
		autoCount := app.orchestrators["agent"].pacingAutoNudgeCount
		app.mu.Unlock()
		if capped {
			t.Fatalf("cycle %d: a session that answered its nudge must not be capped", i)
		}
		if count != 0 {
			t.Fatalf("cycle %d: expected the reply to rearm the nudge count, got count=%d", i, count)
		}
		// The cap's own gauge rearms every cycle, but the cumulative history
		// keeps climbing past orchestratorPacingMaxNudges without ever
		// capping -- the bound and the record are two different questions.
		if want := i + 1; autoCount != want {
			t.Fatalf("cycle %d: expected the cumulative auto-nudge history to reach %d, got %d", i, want, autoCount)
		}
	}

	if notices := emits.events(appNotificationEvent); len(notices) != 0 {
		t.Fatalf("expected no capped notification for a session that answered every nudge, got %d", len(notices))
	}
}

// TestOrchestratorPacingStillCapsWithoutAFreshReply is the control for
// TestOrchestratorPacingRearmsOnIdleReplyAcrossManyStaleCycles: a session that
// never writes an activity report newer than its last nudge still caps, so
// the rearm fix does not turn the cap into a no-op for a genuinely silent
// session.
func TestOrchestratorPacingStillCapsWithoutAFreshReply(t *testing.T) {
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
	app.mu.Unlock()
	if !capped {
		t.Fatal("expected a session that never replies to still cap")
	}
	if notices := emits.events(appNotificationEvent); len(notices) != 1 {
		t.Fatalf("expected exactly one capped notification, got %d", len(notices))
	}
}

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

// TestSendOrchestratorPacingNudgeSkipsWhilePaneBeingTypedInto pins the third
// place #1330's hazard applies: the pacing nudge writes its text, then a bare
// carriage return 150ms later, the same shape as the AI repaint resize that
// corrupted a submitted prompt. Firing it into a pane mid-sentence would glue
// the pacing text onto whatever the operator is typing. The skip must not
// cost against the nudge cap: it is deferred, not counted, so the reconciler
// can retry once the operator pauses.
func TestSendOrchestratorPacingNudgeSkipsWhilePaneBeingTypedInto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

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

	// First stale tick: no recent input, so the reconciler nudges normally.
	app.reconcileOrchestratorPacing()
	firstCalls := session.Calls()
	if len(firstCalls) != 2 {
		t.Fatalf("expected the first stale tick to nudge, got %v", firstCalls)
	}
	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if count != 1 {
		t.Fatalf("expected one recorded nudge, got %d", count)
	}

	// The operator starts typing into the pane between ticks.
	app.mu.Lock()
	managed.lastInputAt = time.Now()
	app.mu.Unlock()

	// A later stale tick must stand down rather than glue the nudge text onto
	// the half-typed line, and must not consume a nudge against the cap.
	app.reconcileOrchestratorPacing()
	if got := session.Calls(); len(got) != len(firstCalls) {
		t.Fatalf("a pane being typed into must not receive a pacing nudge, got extra writes %v", got[len(firstCalls):])
	}
	app.mu.Lock()
	count = app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if count != 1 {
		t.Fatalf("skipping for input must not consume a nudge from the cap, got count=%d", count)
	}
	if !strings.Contains(logs.String(), "pacing nudge deferred: pane is being typed into") {
		t.Fatalf("expected the typing-deferred skip to be logged, got:\n%s", logs.String())
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

// TestOrchestratorPacingMarkerReportsMeasuredStaleness is the regression test
// for erun#1376's first defect: the marker used to format the
// orchestratorPacingStaleAfter constant (always "10m0s") regardless of how
// long the session had actually been quiet, so a 20-minute outage rendered as
// the ten-minute contract being honoured. This drives the reconciler with a
// report aged ~20 minutes — double the contract — and asserts the marker
// names the measured gap rather than the constant. Quoted both ways: on the
// pre-fix code (marker built from orchestratorPacingStaleAfter) this fails
// because the rendered text is always "10m0s"; post-fix it names the real gap.
func TestOrchestratorPacingMarkerReportsMeasuredStaleness(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 7, kind: sessionKindOrchestrator}
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: time.Now().Add(-30 * time.Minute)}
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-20 * time.Minute).Unix()})

	app.reconcileOrchestratorPacing()

	texts := orchestratorPacingMarkerTexts(emits)
	if len(texts) != 1 {
		t.Fatalf("expected exactly one marker, got %v", texts)
	}
	if strings.Contains(texts[0], orchestratorPacingStaleAfter.String()) {
		t.Fatalf("marker still names the constant %s instead of the measured gap: %q", orchestratorPacingStaleAfter, texts[0])
	}
	if !strings.Contains(texts[0], "20m") {
		t.Fatalf("expected the marker to name the measured ~20m gap, got %q", texts[0])
	}
}

// TestOrchestratorPacingMarkerDistinguishesADiedTurnFromAQuietOne is the
// regression test for erun#1376's fourth requirement: a session whose last
// report said "busy" and was never followed by an idle one reads differently
// from one that finished its turn and then went quiet, and differently again
// from one that never wrote a report at all. The observed incident had a
// report stuck on busy=true from a turn that died on a dropped connection —
// this is what should have told the operator that from the pane alone.
func TestOrchestratorPacingMarkerDistinguishesADiedTurnFromAQuietOne(t *testing.T) {
	cases := []struct {
		name        string
		writeReport bool
		busy        bool
		wantSubstr  string
	}{
		{"last report said busy: reads as a died turn", true, true, "may have died"},
		{"last report said idle: reads as an ordinary quiet pane", true, false, "no activity report for"},
		{"no report was ever written: reads as never reported", false, false, "since it started"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			orchestratorPacingNudgeSettle = 0

			app := NewApp(erunUIDeps{})
			emits := newCapturedEmits()
			app.SetEmitter(emits.fn())

			session := newCallRecordingSession()
			key := orchestratorSessionKey("agent")
			app.sessions[key] = &managedTerminal{session: session, key: key, serial: 7, kind: sessionKindOrchestrator}
			started := time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)
			app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: started}
			if tc.writeReport {
				writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: tc.busy, AtUnix: started.Unix()})
			}

			app.reconcileOrchestratorPacing()

			texts := orchestratorPacingMarkerTexts(emits)
			if len(texts) != 1 {
				t.Fatalf("expected exactly one marker, got %v", texts)
			}
			if !strings.Contains(texts[0], tc.wantSubstr) {
				t.Fatalf("marker %q does not contain %q", texts[0], tc.wantSubstr)
			}
		})
	}
}

// TestOrchestratorPacingLogsEachDecisionReasonOnTransition is the fix for
// erun#1376's second defect: every branch decideOrchestratorPacing can reach —
// not just the ones that end in a nudge — leaves a durable, findable record of
// the reason. Before this, a stalled pane and a healthy quiet one were
// indistinguishable because nothing recorded which one applied. It also pins
// that a second tick with nothing changed does not repeat any line, since a
// per-tick log would drown the signal for orchestrators that spend most of
// their life "fresh".
func TestOrchestratorPacingLogsEachDecisionReasonOnTransition(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := NewApp(erunUIDeps{})

	freshSession := newCallRecordingSession()
	freshKey := orchestratorSessionKey("fresh")
	app.sessions[freshKey] = &managedTerminal{session: freshSession, key: freshKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	app.orchestrators["gone"] = &orchestratorSession{id: "gone", serial: 2, name: "gone", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	cappedSession := newCallRecordingSession()
	cappedKey := orchestratorSessionKey("capped")
	app.sessions[cappedKey] = &managedTerminal{session: cappedSession, key: cappedKey, serial: 3, kind: sessionKindOrchestrator}
	app.orchestrators["capped"] = &orchestratorSession{
		id: "capped", serial: 3, name: "capped",
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		pacingCapped: true,
	}

	// id != name, the shape that was reported: the log names the id first and
	// carries the display name beside it.
	nudgedSession := newCallRecordingSession()
	nudgedKey := orchestratorSessionKey("petios")
	app.sessions[nudgedKey] = &managedTerminal{session: nudgedSession, key: nudgedKey, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["petios"] = &orchestratorSession{id: "petios", serial: 4, name: "petios-qa", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	// The id leads because it is the key the state files use; the display name
	// follows parenthetically only when it differs. The fixtures that keep
	// id == name pin that the common case still reads exactly as it did before,
	// so a redundant "fresh (fresh)" cannot creep in.
	for _, want := range []string{
		"orchestrator fresh pacing decision=fresh",
		"orchestrator gone pacing decision=not-alive",
		"orchestrator capped pacing decision=already-capped",
		"orchestrator petios (petios-qa) pacing decision=nudge",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected %q in the pacing log, got:\n%s", want, logged)
		}
	}

	// A second tick with none of these reasons changed must not repeat any line.
	logs.Reset()
	app.reconcileOrchestratorPacing()
	if logs.Len() != 0 {
		t.Fatalf("expected no repeated pacing log lines on an unchanged reason, got:\n%s", logs.String())
	}
}

// TestOrchestratorPacingLogLineJoinsItsNudgeHistoryRecord pins that a pacing
// log line and a nudge history record about the same orchestrator are matchable
// without reading config.yaml. The pacing log named the config `name:` while
// orchestrator-nudge-history.json keys by `id:`, so an
// orchestrator whose pair differs — petios / petios-qa on the reporting host —
// appeared under two strings on two surfaces with nothing saying so, and the
// state file read as silently dropping nudges.
func TestOrchestratorPacingLogLineJoinsItsNudgeHistoryRecord(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := NewApp(erunUIDeps{})
	historyPath := app.deps.orchestratorNudgeHistoryPath
	if historyPath == "" {
		t.Fatal("expected the app to resolve a nudge history path")
	}

	session := newCallRecordingSession()
	key := orchestratorSessionKey("petios")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["petios"] = &orchestratorSession{
		id: "petios", serial: 4, name: "petios-qa",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()

	const wantLine = "orchestrator petios (petios-qa) pacing decision=nudge"
	if !strings.Contains(logs.String(), wantLine) {
		t.Fatalf("expected %q in the pacing log, got:\n%s", wantLine, logs.String())
	}
	if _, found, unreadable := orchestratorNudgeHistoryFor(historyPath, "petios"); !found || unreadable {
		t.Fatalf("expected a nudge history record under id %q (found=%v unreadable=%v), so the log line joins its state record",
			"petios", found, unreadable)
	}
	if _, found, _ := orchestratorNudgeHistoryFor(historyPath, "petios-qa"); found {
		t.Fatalf("nudge history must be keyed by the id, not by the display name %q", "petios-qa")
	}
}

// The gap this covers: reconcileOrchestratorPacing iterated the desktop's own
// session map, so a configured orchestrator whose session this desktop never
// launched -- one started in a terminal, or by a previous desktop instance --
// had no row, was never reconciled, and never appeared in this log at all. Its
// nudge count simply froze, indistinguishable from a session that needed no
// nudge. The log's whole purpose is that a quiet pane and a suppressed one can
// be told apart from it, and an orchestrator that is never evaluated is less
// legible than one that is suppressed -- the inverse of what it exists for.
//
// The reason is stated in full in the assertion below on purpose: it is the
// transport's own reason, never "not-alive", because these sessions are usually
// alive and reporting.
func TestOrchestratorPacingLogsAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	// One orchestrator this desktop holds a session for, so the contrast is in
	// the same log rather than asserted from a different run.
	createOrchestratorNamed(t, app, "held")
	heldSession := newCallRecordingSession()
	heldKey := orchestratorSessionKey("held")
	app.sessions[heldKey] = &managedTerminal{session: heldSession, key: heldKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["held"] = &orchestratorSession{id: "held", serial: 1, name: "held", startedAt: time.Now()}

	// And one it does not, which is the case that used to be silent.
	createOrchestratorNamed(t, app, "external")

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	for _, want := range []string{
		"orchestrator held pacing decision=fresh",
		"orchestrator external pacing decision=unreachable-from-transport quiet=unknown",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected %q in the pacing log, got:\n%s", want, logged)
		}
	}
	// Distinguishable from the two states it used to be conflated with: a
	// session that is genuinely fresh, and one that has already been capped.
	// The unreached orchestrator never reported at all, so neither applies to
	// it, and saying either would be a claim nobody here observed.
	for _, unwanted := range []string{
		"orchestrator external pacing decision=fresh",
		"orchestrator external pacing decision=already-capped",
	} {
		if strings.Contains(logged, unwanted) {
			t.Fatalf("expected no %q, got:\n%s", unwanted, logged)
		}
	}

	// Same once-per-transition rule as every other row: a tick with nothing
	// changed repeats nothing. Without that, this population -- which never
	// changes reason -- would write a line every 15 seconds forever.
	logs.Reset()
	app.reconcileOrchestratorPacing()
	if strings.Contains(logs.String(), "orchestrator external pacing") {
		t.Fatalf("expected the unreachable decision not to repeat on an unchanged tick, got:\n%s", logs.String())
	}
}

// The other half of the line's honesty: when the orchestrator's own hooks ARE
// reporting, the quiet period is the age of that report rather than "unknown".
// The report is what makes it visible to the desktop in the first place, so a
// line that could not measure it would be the least useful case named.
func TestOrchestratorPacingUnmanagedRowReportsTheAgeOfItsOwnReport(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	createOrchestratorNamed(t, app, "external")
	writeOrchestratorActivity(t, "external", orchestratorActivity{
		Busy:   false,
		AtUnix: time.Now().Add(-20 * time.Minute).Unix(),
	})

	app.reconcileOrchestratorPacing()

	// The report's own age, to the second: pinning the exact instant would only
	// re-measure the clock this test runs on.
	want := regexp.MustCompile(`orchestrator external pacing decision=unreachable-from-transport quiet=20m\d+s`)
	if !want.MatchString(logs.String()) {
		t.Fatalf("expected %s in the pacing log, got:\n%s", want, logs.String())
	}
}

// Nothing is attempted against a session the desktop does not own: the decision
// is none/unreachable, and it is reached before any of the acting paths (a
// rearm, a cap, a write into a pty) are considered. Nudging it is not possible
// from here, and pretending otherwise would be worse than the gap.
func TestOrchestratorPacingNeverNudgesAConfiguredOrchestratorItDoesNotOwn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	createOrchestratorNamed(t, app, "external")
	writeOrchestratorActivity(t, "external", orchestratorActivity{
		Busy:   false,
		AtUnix: time.Now().Add(-2 * time.Hour).Unix(),
	})

	rows := app.orchestratorPacingUnmanagedRows(map[string]struct{}{})
	if len(rows) != 1 || rows[0].id != "external" || !rows[0].unmanaged {
		t.Fatalf("expected one unmanaged row for the configured orchestrator, got %+v", rows)
	}
	decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), false)
	if decision != orchestratorPacingNone || reason != orchestratorPacingReasonUnreachable {
		t.Fatalf("expected none/unreachable for a session this desktop does not own, got %v/%v", decision, reason)
	}
	// An explicit whip does not change that either: the operator asking does
	// not create a pty to write into.
	if decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), true); decision != orchestratorPacingNone || reason != orchestratorPacingReasonUnreachable {
		t.Fatalf("expected none/unreachable for an explicit whip too, got %v/%v", decision, reason)
	}
}

// The hover card's could-not half. A configured orchestrator whose own hooks
// are reporting, but which this desktop holds no session for, is being driven
// somewhere else -- and erun will never pace it from here. Its nudge count
// stays frozen at zero, which is exactly what a freshly checked, needing-nothing
// orchestrator looks like, so the card has to be able to say which one this is.
func TestListOrchestratorsMarksAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	reported := createOrchestratorNamed(t, app, "external")
	quiet := createOrchestratorNamed(t, app, "stopped")
	writeOrchestratorActivity(t, reported, orchestratorActivity{Busy: true, AtUnix: time.Now().Add(-time.Minute).Unix()})
	// Old enough that the pacer would no longer be deciding for it either.
	writeOrchestratorActivity(t, quiet, orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-time.Hour).Unix()})

	byID := map[string]orchestratorInfo{}
	for _, info := range app.ListOrchestrators() {
		byID[info.ID] = info
	}
	if !byID[reported].PacingUnreachable {
		t.Fatalf("expected %s to be marked as unpaced from this desktop, got %+v", reported, byID[reported])
	}
	if byID[quiet].PacingUnreachable {
		t.Fatalf("expected a stopped orchestrator nothing is reporting for to stay unmarked, got %+v", byID[quiet])
	}
	for _, info := range byID {
		if info.Status != "stopped" {
			t.Fatalf("expected every orchestrator here to be stopped, got %+v", info)
		}
	}
}

// The mark is about a session this desktop cannot pace, never about the
// orchestrator's own records: one it holds a session for is paced here however
// recently that session reported.
func TestListOrchestratorsNeverMarksAnOrchestratorItHoldsASessionFor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createAndStartOrchestrator(t, app)
	writeOrchestratorActivity(t, id, orchestratorActivity{Busy: true, AtUnix: time.Now().Unix()})

	info := requireOrchestratorInfo(t, app, id)
	if info.Status != "running" || info.PacingUnreachable {
		t.Fatalf("expected a running, paced orchestrator, got %+v", info)
	}
}

// createOrchestratorNamed persists one configured orchestrator without starting
// it: the definition is what the pacer has to decide for even when this desktop
// holds no session of its own, which is the population under test here.
func createOrchestratorNamed(t *testing.T, app *App, name string) string {
	t.Helper()
	created, err := app.CreateOrchestrator(name, []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "")
	if err != nil {
		t.Fatalf("CreateOrchestrator(%q) failed: %v", name, err)
	}
	return created.ID
}

// requireOrchestratorInfo is the listed info for one orchestrator, failing
// rather than returning a zero value when the list does not carry it at all.
func requireOrchestratorInfo(t *testing.T, app *App, id string) orchestratorInfo {
	t.Helper()
	for _, info := range app.ListOrchestrators() {
		if info.ID == id {
			return info
		}
	}
	t.Fatalf("ListOrchestrators did not list %q", id)
	return orchestratorInfo{}
}
