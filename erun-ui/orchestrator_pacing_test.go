package main

import (
	"bytes"
	"encoding/base64"
	"log"
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
			orchestratorPacingCandidate{alive: true, lastActiveAt: stale}, orchestratorPacingNudge, orchestratorPacingReasonNudge,
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

	nudgedSession := newCallRecordingSession()
	nudgedKey := orchestratorSessionKey("nudged")
	app.sessions[nudgedKey] = &managedTerminal{session: nudgedSession, key: nudgedKey, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["nudged"] = &orchestratorSession{id: "nudged", serial: 4, name: "nudged", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	for _, want := range []string{
		"orchestrator fresh pacing decision=fresh",
		"orchestrator gone pacing decision=not-alive",
		"orchestrator capped pacing decision=already-capped",
		"orchestrator nudged pacing decision=nudge",
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
