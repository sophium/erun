package main

import (
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// pacingEnvBusyTestApp builds one orchestrator, alive and stale (its own
// activity report has not moved since well past the staleness window), linked
// to a single environment "acme/dev". Callers stage app.envActivity for that
// environment (or leave it unset for the never-observed case) before driving
// the reconciler.
func pacingEnvBusyTestApp(t *testing.T, startedAt time.Time) (*App, *callRecordingSession) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 5, kind: sessionKindOrchestrator}
	app.orchestrators["agent"] = &orchestratorSession{
		id:        "agent",
		serial:    5,
		name:      "agent",
		startedAt: startedAt,
		envs:      []eruncommon.OrchestratorEnvConfig{{Tenant: "acme", Environment: "dev"}},
	}
	return app, session
}

// TestOrchestratorPacingSuppressedWhileLinkedEnvBusy is the acceptance case
// the issue opens with: a linked environment busy on a lease this
// orchestrator holds means the orchestrator is waiting, not idle. It must not
// be nudged and must not accrue toward the cap.
func TestOrchestratorPacingSuppressedWhileLinkedEnvBusy(t *testing.T) {
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: true, detail: "holding: 1682 desktop ux and co", busyHolderOrchestrators: "agent"},
	}

	rows := app.orchestratorPacingRows()
	if len(rows) != 1 {
		t.Fatalf("expected one pacing row, got %d", len(rows))
	}
	decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), false)
	if decision != orchestratorPacingNone || reason != orchestratorPacingReasonEnvBusy {
		t.Fatalf("expected decision=none reason=env-busy, got decision=%v reason=%v", decision, reason)
	}
	if len(session.Calls()) != 0 {
		t.Fatalf("expected no nudge written while the linked env is busy, got %v", session.Calls())
	}
	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	app.mu.Unlock()
	if count != 0 {
		t.Fatalf("expected the nudge count to stay at zero while suppressed, got %d", count)
	}
}

// TestOrchestratorPacingBusySuppressionOnlyCountsLinkedEnvironments guards the
// scope: an unrelated environment being busy on this orchestrator's lease
// must not suppress pacing for an orchestrator that never linked it. An
// orchestrator is not excused by some other environment's activity.
func TestOrchestratorPacingBusySuppressionOnlyCountsLinkedEnvironments(t *testing.T) {
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	unrelatedKey := selectionKey(uiSelection{Tenant: "other", Environment: "prod"})
	app.envActivity = map[string]environmentActivityState{
		unrelatedKey: {observed: true, busy: true, detail: "holding: something-else", busyHolderOrchestrators: "agent"},
	}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected the ordinary nudge to fire since the busy env is not linked, got %v", session.Calls())
	}
}

// TestOrchestratorPacingBusySuppressionOnlyCountsThisOrchestratorsLease guards
// the holder discriminator: a linked environment busy on another
// orchestrator's job, or an operator's own session, must not suppress this
// orchestrator's nudge — otherwise a shared environment would silence every
// orchestrator linked to it.
func TestOrchestratorPacingBusySuppressionOnlyCountsThisOrchestratorsLease(t *testing.T) {
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: true, detail: "holding: someone-elses-job", busyHolderOrchestrators: "another-orchestrator"},
	}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected the ordinary nudge to fire since the lease belongs to a different orchestrator, got %v", session.Calls())
	}
}

// TestOrchestratorPacingAllLinkedEnvsIdlePacesAsToday is the control: every
// linked environment observed and none busy behaves exactly like the
// env-unaware pass did before this change.
func TestOrchestratorPacingAllLinkedEnvsIdlePacesAsToday(t *testing.T) {
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: false},
	}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected the ordinary nudge with every linked env idle, got %v", session.Calls())
	}
}

// TestOrchestratorPacingUnknownEnvActivityFallsBackToBaseSignal pins the
// explicit decision for the undetermined case: a linked environment whose
// activity was never observed (poller has not reached it, env stopped, edge
// unreachable) must resolve to neither "idle" (which would nudge on a false
// assertion of no activity) nor "busy" (which would mask a genuinely dark
// orchestrator). It falls back to the base, env-unaware decision — the same
// nudge behaviour as if this orchestrator had never linked an environment at
// all — rather than silently defaulting to one side.
func TestOrchestratorPacingUnknownEnvActivityFallsBackToBaseSignal(t *testing.T) {
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	// app.envActivity is left nil: the poller has never observed "acme/dev".

	rows := app.orchestratorPacingRows()
	envBusy := orchestratorLinkedEnvBusyStateFor(rows[0].id, rows[0].envs, app.envActivitySnapshot())
	if envBusy.signal != orchestratorEnvActivityUnknown {
		t.Fatalf("expected the unobserved linked env to read as unknown, got %v", envBusy.signal)
	}

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected the base nudge to fire for an unknown linked env (not suppressed, not forced), got %v", session.Calls())
	}
}

// TestOrchestratorPacingSurfacesPastTheEnvBusyBound is the second acceptance
// case: a lane that never finishes must not silence its orchestrator forever.
// Once the orchestrator has gone unanswered longer than
// orchestratorPacingEnvBusyBound, the reconciler stops excusing the silence
// and nudges anyway, describing the linked environment rather than reading as
// an ordinary quiet pane.
func TestOrchestratorPacingSurfacesPastTheEnvBusyBound(t *testing.T) {
	bound := orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-bound-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: true, detail: "holding: a build that never finished", busyHolderOrchestrators: "agent"},
	}

	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	app.reconcileOrchestratorPacing()

	if len(session.Calls()) != 2 {
		t.Fatalf("expected the orchestrator to be surfaced past the bound despite the busy env, got %v", session.Calls())
	}
	texts := orchestratorPacingMarkerTexts(emits)
	if len(texts) != 1 {
		t.Fatalf("expected exactly one marker, got %v", texts)
	}
	if want := "a build that never finished"; !strings.Contains(texts[0], want) {
		t.Fatalf("expected the marker to name the stuck environment (%q), got %q", want, texts[0])
	}
}

// TestOrchestratorPacingBusySuppressionDoesNotConsumeTheCap is the cap
// interaction the issue calls out directly: waiting correctly through a long
// run must not count toward orchestratorPacingMaxNudges, so an orchestrator
// that later needs the ordinary nudge/cap contract still gets the full
// budget rather than one already spent on suppressed ticks.
func TestOrchestratorPacingBusySuppressionDoesNotConsumeTheCap(t *testing.T) {
	bound := orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: true, detail: "holding: a long gate run", busyHolderOrchestrators: "agent"},
	}

	// Many ticks well within the bound: none may nudge or spend the cap,
	// however long the run drags on.
	rows := app.orchestratorPacingRows()
	tick := time.Now()
	for i := 0; i < orchestratorPacingMaxNudges*3; i++ {
		tick = tick.Add(time.Minute)
		if tick.Sub(rows[0].startedAt) >= bound {
			t.Fatalf("test setup drifted past the suppression bound before asserting the cap stayed unspent")
		}
		decision, reason := app.reconcileOrchestratorPacingOne(rows[0], tick, false)
		if decision != orchestratorPacingNone || reason != orchestratorPacingReasonEnvBusy {
			t.Fatalf("tick %d: expected continued suppression, got decision=%v reason=%v", i, decision, reason)
		}
	}
	if len(session.Calls()) != 0 {
		t.Fatalf("expected no writes across every suppressed tick, got %v", session.Calls())
	}
	app.mu.Lock()
	count := app.orchestrators["agent"].pacingNudgeCount
	capped := app.orchestrators["agent"].pacingCapped
	app.mu.Unlock()
	if count != 0 || capped {
		t.Fatalf("expected the cap to stay entirely unspent through the busy period, got count=%d capped=%v", count, capped)
	}
}

// TestOrchestratorLinkedEnvBusyStateForDiscriminatesByHolder is the pure-logic
// unit test for the aggregation the reconciler drives: busy beats unknown,
// unknown beats idle, and only this orchestrator's own lease holder counts as
// busy.
func TestOrchestratorLinkedEnvBusyStateForDiscriminatesByHolder(t *testing.T) {
	envs := []eruncommon.OrchestratorEnvConfig{
		{Tenant: "acme", Environment: "dev"},
		{Tenant: "acme", Environment: "build"},
	}
	devKey := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	buildKey := selectionKey(uiSelection{Tenant: "acme", Environment: "build"})

	cases := []struct {
		name     string
		snapshot map[string]environmentActivityState
		want     orchestratorEnvActivitySignal
	}{
		{
			"both idle and observed",
			map[string]environmentActivityState{
				devKey:   {observed: true},
				buildKey: {observed: true},
			},
			orchestratorEnvActivityIdle,
		},
		{
			"one busy for this orchestrator, one idle",
			map[string]environmentActivityState{
				devKey:   {observed: true},
				buildKey: {observed: true, busy: true, busyHolderOrchestrators: "agent"},
			},
			orchestratorEnvActivityBusy,
		},
		{
			"one busy for a different orchestrator, one idle: not busy",
			map[string]environmentActivityState{
				devKey:   {observed: true},
				buildKey: {observed: true, busy: true, busyHolderOrchestrators: "someone-else"},
			},
			orchestratorEnvActivityIdle,
		},
		{
			"one never observed: unknown even though the other is idle",
			map[string]environmentActivityState{
				devKey: {observed: true},
			},
			orchestratorEnvActivityUnknown,
		},
		{
			"one busy for this orchestrator beats an unknown sibling",
			map[string]environmentActivityState{
				devKey:   {observed: true, busy: true, busyHolderOrchestrators: "agent"},
				buildKey: {},
			},
			orchestratorEnvActivityBusy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orchestratorLinkedEnvBusyStateFor("agent", envs, tc.snapshot)
			if got.signal != tc.want {
				t.Fatalf("orchestratorLinkedEnvBusyStateFor() = %v, want %v", got.signal, tc.want)
			}
		})
	}
}
