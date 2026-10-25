package orchestrator

import (
	"bytes"
	"encoding/base64"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func orchestratorPacingMarkerTexts(emits *capturedEmits) []string {
	var texts []string
	for _, evt := range emits.events(uimodel.TerminalOutputEvent) {
		payload, ok := evt.(uimodel.TerminalOutputPayload)
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

// callRecordingSession records each Write separately.
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

func TestReconcileOrchestratorPacingNudgesAQuietSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orig := orchestratorPacingNudgeSettle
	orchestratorPacingNudgeSettle = 0
	defer func() { orchestratorPacingNudgeSettle = orig }()

	app := newTestApp(testDeps{})
	session := newCallRecordingSession()
	app.terminals.seed("agent", 5, session)
	app.orchestrators["agent"] = &orchestratorSession{
		id:        "agent",
		serial:    5,
		name:      "agent",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.ReconcileOrchestratorPacing()

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

	_, unlock := lockTerminalForTest(app)
	count := app.orchestrators["agent"].pacingNudgeCount
	unlock()
	if count != 1 {
		t.Fatalf("expected the nudge count to be recorded, got %d", count)
	}
}

func TestReconcileOrchestratorPacingSkipsFreshAndTransient(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})

	freshSession := newCallRecordingSession()
	app.terminals.seed("fresh", 1, freshSession)
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, startedAt: time.Now()}

	transientSession := newCallRecordingSession()
	app.terminals.seed("", 3, transientSession)
	app.orchestrators[""] = &orchestratorSession{
		serial: 3, transient: true,
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.ReconcileOrchestratorPacing()

	for name, s := range map[string]*callRecordingSession{"fresh": freshSession, "transient": transientSession} {
		if len(s.Calls()) != 0 {
			t.Fatalf("%s session must not be nudged, got writes %v", name, s.Calls())
		}
	}
}

func TestReconcileOrchestratorPacingNudgesThroughABackgroundShell(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	session := newCallRecordingSession()
	app.terminals.seed("shell", 2, session)
	app.orchestrators["shell"] = &orchestratorSession{
		id: "shell", serial: 2,
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		shellRunning: true,
	}

	app.ReconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected a background shell to no longer suppress the nudge, got writes %v", session.Calls())
	}
}

func TestOrchestratorPacingCapsAfterRepeatedSilenceAndRearmsOnFreshBusy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	app.terminals.seed("agent", 5, session)
	longAgo := time.Now().Add(-24 * time.Hour)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: longAgo}

	for i := 0; i < orchestratorPacingMaxNudges; i++ {
		app.ReconcileOrchestratorPacing()
	}
	_, unlock := lockTerminalForTest(app)
	count := app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	unlock()
	if count != orchestratorPacingMaxNudges || capped {
		t.Fatalf("expected %d nudges and no cap yet, got count=%d capped=%v", orchestratorPacingMaxNudges, count, capped)
	}
	writesBeforeCap := len(session.Calls())

	app.ReconcileOrchestratorPacing()
	_, unlock = lockTerminalForTest(app)
	capped = app.orchestrators["agent"].pacingCapped
	unlock()
	if !capped {
		t.Fatal("expected the cap to latch after the max nudges")
	}
	if len(session.Calls()) != writesBeforeCap {
		t.Fatalf("a capped orchestrator must not be written to again, got %d new writes", len(session.Calls())-writesBeforeCap)
	}
	notices := emits.events(uimodel.AppNotificationEvent)
	if len(notices) != 1 {
		t.Fatalf("expected exactly one capped notification, got %d", len(notices))
	}

	app.ReconcileOrchestratorPacing()
	if len(emits.events(uimodel.AppNotificationEvent)) != 1 {
		t.Fatal("the capped notice must not repeat every tick")
	}

	// Derive AtUnix from the last nudge: both are whole seconds, and the real
	// clock could land in the same second.
	_, unlock = lockTerminalForTest(app)
	lastNudgeAtUnix := app.orchestrators["agent"].pacingLastNudgeAtUnix
	unlock()
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: true, AtUnix: lastNudgeAtUnix + 10})
	app.ReconcileOrchestratorPacing()
	_, unlock = lockTerminalForTest(app)
	capped = app.orchestrators["agent"].pacingCapped
	count = app.orchestrators["agent"].pacingNudgeCount
	unlock()
	if capped || count != 0 {
		t.Fatalf("a fresh busy report must rearm the cap, got capped=%v count=%d", capped, count)
	}
}

// A synthetic clock drives many more stale-then-reply cycles than the cap allows.
func TestOrchestratorPacingRearmsOnIdleReplyAcrossManyStaleCycles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	app.terminals.seed("agent", 5, session)
	base := time.Unix(1_700_000_000, 0)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: base}

	cycles := orchestratorPacingMaxNudges + 2
	tick := base
	for i := 0; i < cycles; i++ {
		tick = tick.Add(orchestratorPacingStaleAfter + time.Minute)

		rows := app.orchestratorPacingRows()
		if len(rows) != 1 {
			t.Fatalf("expected one pacing row, got %d", len(rows))
		}
		decision, _ := app.reconcileOrchestratorPacingOne(rows[0], tick, false)
		if decision != orchestratorPacingNudge {
			t.Fatalf("cycle %d: expected a nudge, got decision=%v", i, decision)
		}

		_, unlock := lockTerminalForTest(app)
		lastNudgeAtUnix := app.orchestrators["agent"].pacingLastNudgeAtUnix
		unlock()

		replyAt := time.Unix(lastNudgeAtUnix, 0).Add(time.Second)
		writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: replyAt.Unix()})

		tick = replyAt.Add(10 * time.Second)
		rows = app.orchestratorPacingRows()
		app.reconcileOrchestratorPacingOne(rows[0], tick, false)

		_, unlock = lockTerminalForTest(app)
		count := app.orchestrators["agent"].pacingNudgeCount
		capped := app.orchestrators["agent"].pacingCapped
		autoCount := app.orchestrators["agent"].pacingAutoNudgeCount
		unlock()
		if capped {
			t.Fatalf("cycle %d: a session that answered its nudge must not be capped", i)
		}
		if count != 0 {
			t.Fatalf("cycle %d: expected the reply to rearm the nudge count, got count=%d", i, count)
		}
		// The gauge rearms every cycle; the cumulative history keeps climbing.
		if want := i + 1; autoCount != want {
			t.Fatalf("cycle %d: expected the cumulative auto-nudge history to reach %d, got %d", i, want, autoCount)
		}
	}

	if notices := emits.events(uimodel.AppNotificationEvent); len(notices) != 0 {
		t.Fatalf("expected no capped notification for a session that answered every nudge, got %d", len(notices))
	}
}

func TestOrchestratorPacingStillCapsWithoutAFreshReply(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	app.terminals.seed("agent", 5, session)
	longAgo := time.Now().Add(-24 * time.Hour)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 5, name: "agent", startedAt: longAgo}

	for i := 0; i < orchestratorPacingMaxNudges+1; i++ {
		app.ReconcileOrchestratorPacing()
	}

	_, unlock := lockTerminalForTest(app)
	capped := app.orchestrators["agent"].pacingCapped
	unlock()
	if !capped {
		t.Fatal("expected a session that never replies to still cap")
	}
	if notices := emits.events(uimodel.AppNotificationEvent); len(notices) != 1 {
		t.Fatalf("expected exactly one capped notification, got %d", len(notices))
	}
}

func TestSendOrchestratorPacingNudgeSkipsWhilePaneBeingTypedInto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := newTestApp(testDeps{})
	session := newCallRecordingSession()
	managed := app.terminals.seed("agent", 5, session)
	app.orchestrators["agent"] = &orchestratorSession{
		id:        "agent",
		serial:    5,
		name:      "agent",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.ReconcileOrchestratorPacing()
	firstCalls := session.Calls()
	if len(firstCalls) != 2 {
		t.Fatalf("expected the first stale tick to nudge, got %v", firstCalls)
	}
	_, unlock := lockTerminalForTest(app)
	count := app.orchestrators["agent"].pacingNudgeCount
	unlock()
	if count != 1 {
		t.Fatalf("expected one recorded nudge, got %d", count)
	}

	managed.typed()

	app.ReconcileOrchestratorPacing()
	if got := session.Calls(); len(got) != len(firstCalls) {
		t.Fatalf("a pane being typed into must not receive a pacing nudge, got extra writes %v", got[len(firstCalls):])
	}
	_, unlock = lockTerminalForTest(app)
	count = app.orchestrators["agent"].pacingNudgeCount
	unlock()
	if count != 1 {
		t.Fatalf("skipping for input must not consume a nudge from the cap, got count=%d", count)
	}
	if !strings.Contains(logs.String(), "pacing nudge deferred: pane is being typed into") {
		t.Fatalf("expected the typing-deferred skip to be logged, got:\n%s", logs.String())
	}
}

// Operator input rearms the cap budget; the cumulative history the hover card shows stays.
func TestRearmOrchestratorPacingClearsTheCapButKeepsTheHistory(t *testing.T) {
	app := newTestApp(testDeps{})
	app.orchestrators["agent"] = &orchestratorSession{
		id: "agent", serial: 1,
		pacingNudgeCount:     orchestratorPacingMaxNudges,
		pacingCapped:         true,
		pacingAutoNudgeCount: orchestratorPacingMaxNudges,
	}

	app.RearmOrchestratorPacing("agent")

	_, unlock := lockTerminalForTest(app)
	session := app.orchestrators["agent"]
	count, capped, history := session.pacingNudgeCount, session.pacingCapped, session.pacingAutoNudgeCount
	unlock()
	if count != 0 || capped {
		t.Fatalf("expected operator input to rearm pacing, got count=%d capped=%v", count, capped)
	}
	if history != orchestratorPacingMaxNudges {
		t.Fatalf("expected the cumulative history kept, got %d", history)
	}
}

// createOrchestratorNamed persists a definition without starting a session.
func createOrchestratorNamed(t *testing.T, app *testApp, name string) string {
	t.Helper()
	created, err := app.CreateOrchestrator(name, []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator(%q) failed: %v", name, err)
	}
	return created.ID
}

func requireOrchestratorInfo(t *testing.T, app *testApp, id string) Info {
	t.Helper()
	for _, info := range app.ListOrchestrators() {
		if info.ID == id {
			return info
		}
	}
	t.Fatalf("ListOrchestrators did not list %q", id)
	return Info{}
}
