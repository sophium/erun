package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
)

// pacingEnvBusyTestApp builds one alive, stale orchestrator linked to "acme/dev".
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
		envs:      []config.OrchestratorEnvConfig{{Tenant: "acme", Environment: "dev"}},
	}
	return app, session
}

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

func TestOrchestratorPacingBusySuppressionDoesNotConsumeTheCap(t *testing.T) {
	bound := orchestratorPacingEnvBusyBound(getOrchestratorWhipConfig())
	app, session := pacingEnvBusyTestApp(t, time.Now().Add(-orchestratorPacingStaleAfter-time.Minute))
	key := selectionKey(uiSelection{Tenant: "acme", Environment: "dev"})
	app.envActivity = map[string]environmentActivityState{
		key: {observed: true, busy: true, detail: "holding: a long gate run", busyHolderOrchestrators: "agent"},
	}

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

func TestOrchestratorLinkedEnvBusyStateForDiscriminatesByHolder(t *testing.T) {
	envs := []config.OrchestratorEnvConfig{
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
