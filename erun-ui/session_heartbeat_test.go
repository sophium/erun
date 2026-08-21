package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSessionRunningFollowsHeartbeatNotStreamSilence locks both directions of
// the live/stale reconciliation the desktop was missing.
//
// The bug: the AI tab's running state was a pure function of stream traffic, so
// a session that stopped printing read as finished even while its program kept
// running in the pod — which is how the pane could show a frozen spinner beside
// a truthful "still running" claim. The fix drives the state from an observed
// heartbeat instead, so silence never decides on its own.
func TestSessionRunningFollowsHeartbeatNotStreamSilence(t *testing.T) {
	tests := []struct {
		name          string
		podRunning    bool
		wantBusyClear bool
	}{
		{
			name:          "quiet session whose program is alive keeps running",
			podRunning:    true,
			wantBusyClear: false,
		},
		{
			name:          "quiet session whose program is gone stops claiming to run",
			podRunning:    false,
			wantBusyClear: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emits := newCapturedEmits()
			app := &App{
				sessions:          make(map[string]*managedTerminal),
				sessionHeartbeats: make(map[string]sessionHeartbeat),
				emitFn:            emits.fn(),
			}
			selection := uiSelection{Tenant: "petios", Environment: "local"}
			managed := &managedTerminal{
				kind:       sessionKindAI,
				selection:  selection,
				key:        "ai\x00petios\x00local",
				serial:     3,
				appSession: "ai",
				// Latched busy, and silent for longer than the idle threshold —
				// exactly the state the old rule would have cleared.
				aiBusyEmitted: true,
				aiLastOutput:  time.Now().Add(-time.Minute),
			}
			app.sessions[managed.key] = managed
			app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(tc.podRunning)

			app.clearAIActivityIfQuiet(managed)

			cleared := len(emits.events(aiActivityEvent)) > 0
			if cleared != tc.wantBusyClear {
				t.Fatalf("busy cleared = %v, want %v (events: %+v)", cleared, tc.wantBusyClear, emits.events(aiActivityEvent))
			}
			if managed.aiBusyEmitted == tc.wantBusyClear {
				t.Fatalf("latch state %v contradicts the emitted signal", managed.aiBusyEmitted)
			}
		})
	}
}

// TestHeartbeatReleasesBusyWhenThePodReportsTheSessionFinished pins the release
// path: once the silence timer has declined to clear a still-running session,
// the heartbeat is the only thing left that can, so it must.
func TestHeartbeatReleasesBusyWhenThePodReportsTheSessionFinished(t *testing.T) {
	emits := newCapturedEmits()
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app := &App{
		sessions:          make(map[string]*managedTerminal),
		sessionHeartbeats: make(map[string]sessionHeartbeat),
		emitFn:            emits.fn(),
	}
	managed := &managedTerminal{
		kind:          sessionKindAI,
		selection:     selection,
		key:           "ai\x00petios\x00local",
		serial:        4,
		appSession:    "ai",
		aiBusyEmitted: true,
		// Still printing: only the pod's answer may end this, not silence.
		aiLastOutput: time.Now(),
	}
	app.sessions[managed.key] = managed

	app.applySessionHeartbeat(selection, uiRuntimeActivity{
		Sessions:        []uiRuntimeSession{{ID: "ai", Running: true, Program: "claude"}},
		SessionsRunning: 1,
	})
	if len(emits.events(aiActivityEvent)) != 0 {
		t.Fatalf("a running session must not be released: %+v", emits.events(aiActivityEvent))
	}

	app.applySessionHeartbeat(selection, uiRuntimeActivity{
		Sessions:        []uiRuntimeSession{{ID: "ai", Running: false}},
		SessionsRunning: 0,
	})
	if len(emits.events(aiActivityEvent)) != 1 {
		t.Fatalf("a finished session must be released once: %+v", emits.events(aiActivityEvent))
	}
	if managed.aiBusyEmitted {
		t.Fatalf("latch must be down after the pod reports the program gone")
	}
}

// TestHeartbeatObservationExpires guards the safety valve: an observation the
// desktop can no longer refresh (env stopped, pod replaced, cluster
// unreachable) must stop holding a session open forever.
func TestHeartbeatObservationExpires(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app := &App{
		sessions:          make(map[string]*managedTerminal),
		sessionHeartbeats: make(map[string]sessionHeartbeat),
		emitFn:            func(string, ...any) {},
	}
	managed := &managedTerminal{
		kind:       sessionKindAI,
		selection:  selection,
		appSession: "ai",
	}
	stale := heartbeatFor(true)
	stale.observedAt = time.Now().Add(-2 * sessionHeartbeatTTL)
	app.sessionHeartbeats[selectionKey(selection)] = stale

	if app.heartbeatSaysRunning(managed) {
		t.Fatalf("an expired observation must not keep a session marked running")
	}
}

// TestRuntimeActivityCountMatchesRunningSessions is the "number and animation
// cannot disagree" contract: the rendered count and the per-session
// running state are read from one probe, so a socket without a live program is
// absent from both.
func TestRuntimeActivityCountMatchesRunningSessions(t *testing.T) {
	probe := "erun-session\tai\t900\tclaude\n" +
		"erun-session\topen-0\t901\tbash\n" +
		"erun-session\topen-3\t0\t\n"

	activity := runtimeActivityFromProbe(uiSelection{Tenant: "petios", Environment: "local"}, probe)
	if activity.SessionsRunning != 2 {
		t.Fatalf("expected 2 running sessions, got %d (%+v)", activity.SessionsRunning, activity.Sessions)
	}
	running := 0
	for _, session := range activity.Sessions {
		if session.Running {
			running++
		}
	}
	if running != activity.SessionsRunning {
		t.Fatalf("rendered count %d disagrees with the per-session state %d", activity.SessionsRunning, running)
	}
	if want := "Live in the pod right now: 2 sessions running (1 socket no longer has a program behind it), 0 MiB held by running processes."; activity.Message != want {
		t.Fatalf("unexpected message:\n got %q\nwant %q", activity.Message, want)
	}
}

// TestSelectionsWithPodSessionsSkipsSessionsWithoutAPodSession keeps the poller
// off environments it has nothing to observe — the Local shell has no pod
// session, so probing its env would exec into a pod that may not exist.
func TestSelectionsWithPodSessionsSkipsSessionsWithoutAPodSession(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	app.sessions["local"] = &managedTerminal{kind: sessionKindLocal, selection: uiSelection{Tenant: "petios", Environment: "local"}}
	app.sessions["ai"] = &managedTerminal{kind: sessionKindAI, appSession: "ai", selection: uiSelection{Tenant: "petios", Environment: "remote"}}
	app.sessions["open"] = &managedTerminal{kind: sessionKindOpen, appSession: "open-0", selection: uiSelection{Tenant: "petios", Environment: "remote"}}

	selections := app.selectionsWithPodSessions()
	if len(selections) != 1 || selections[0].Environment != "remote" {
		t.Fatalf("expected only the env with pod sessions, got %+v", selections)
	}
}

func heartbeatFor(running bool) sessionHeartbeat {
	heartbeat := sessionHeartbeat{
		observedAt: time.Now(),
		running:    map[string]struct{}{},
		sessions:   map[string]struct{}{"ai": {}},
	}
	if running {
		heartbeat.running["ai"] = struct{}{}
	}
	return heartbeat
}

// TestReclaimRuntimeResourcesRunsTheNamedActionOnly pins the read-only-by-
// default contract: nothing is reclaimed unless the operator names an action,
// and an unknown action is refused rather than guessed at.
func TestReclaimRuntimeResourcesRunsTheNamedActionOnly(t *testing.T) {
	var ran []string
	app := NewApp(erunUIDeps{
		execRuntimePod: func(_ context.Context, _ uiSelection, script string) (string, error) {
			ran = append(ran, script)
			return "", nil
		},
	})
	app.SetEmitter(func(string, ...any) {})

	if _, err := app.ReclaimRuntimeResources(uiRuntimeReclaimInput{Tenant: "petios", Environment: "local", Action: "delete-everything"}); err == nil {
		t.Fatalf("an unknown reclaim action must be refused")
	}
	if len(ran) != 0 {
		t.Fatalf("a refused action must run nothing in the pod: %+v", ran)
	}

	result, err := app.ReclaimRuntimeResources(uiRuntimeReclaimInput{Tenant: "petios", Environment: "local", Action: runtimeReclaimGradleDaemons})
	if err != nil {
		t.Fatalf("ReclaimRuntimeResources: %v", err)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "gradle --stop") || !strings.Contains(ran[0], "Gradle[D]aemon") {
		t.Fatalf("unexpected reclaim script: %+v", ran)
	}
	if result.Message == "" {
		t.Fatalf("a completed reclaim must report what it did")
	}
}

// TestOrchestratorSnapshotRendersBusyWithoutTheEvent locks the first half of
// the #1087 fix: orchestratorInfo carries Busy directly, so a snapshot taken
// after the state changed reflects it even when the ai-activity event that
// announced the change was never observed. That is the path a frontend
// remount, a window reopen, or a listener that attached a beat late actually
// takes in production — none of them re-run the transition, they just ask for
// the current state, so the assertion here deliberately never looks at the
// emitted events, only at what a fresh ListOrchestrators/runningOrchestratorInfo
// call reports.
func TestOrchestratorSnapshotRendersBusyWithoutTheEvent(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if started.Busy {
		t.Fatalf("a freshly started orchestrator must not read busy before any report: %+v", started)
	}

	writeOrchestratorActivity(t, created.ID, orchestratorActivity{Busy: true, AtUnix: time.Now().Unix()})
	app.reconcileOrchestratorActivity()

	listed := app.ListOrchestrators()
	if len(listed) != 1 || !listed[0].Busy {
		t.Fatalf("expected the listed orchestrator to render busy from the snapshot, got %+v", listed)
	}
	info, ok := app.runningOrchestratorInfo(created.ID)
	if !ok || !info.Busy {
		t.Fatalf("expected the running snapshot to carry busy, got %+v (ok=%v)", info, ok)
	}
}

// TestReconcileOrchestratorActivityReEmitsEveryTick locks the second half of
// the #1087 fix: the busy signal is republished on every tick regardless of
// whether it changed, so a dropped or mistimed ai-activity event self-heals
// within one tick instead of staying wrong until the busy state itself next
// changes. The old code's `if busy == r.busy { continue }` would have emitted
// once here, not three times.
func TestReconcileOrchestratorActivityReEmitsEveryTick(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorActivity(t, created.ID, orchestratorActivity{Busy: true, AtUnix: time.Now().Unix()})

	app.reconcileOrchestratorActivity()
	app.reconcileOrchestratorActivity()
	app.reconcileOrchestratorActivity()

	events := emits.events(aiActivityEvent)
	if len(events) != 3 {
		t.Fatalf("expected one emit per tick even with no state change, got %d: %+v", len(events), events)
	}
	for _, event := range events {
		payload, ok := event.(aiActivityPayload)
		if !ok || !payload.Busy {
			t.Fatalf("expected every re-emit to report busy=true, got %+v", event)
		}
	}
}

// TestOrchestratorShellSnapshotRendersRunningWithoutTheEvent is the shell-report
// half of the same busy-snapshot treatment: orchestratorInfo carries
// ShellRunning directly, so a snapshot taken after the state changed reflects
// it even when the orchestrator-shell-activity event that announced the
// change was never observed — the same remount/reopen/late-listener path
// TestOrchestratorSnapshotRendersBusyWithoutTheEvent locks for the turn's own
// busy signal, but for a fact that is independent of it: a shell can be
// running while the turn itself already reads idle.
func TestOrchestratorShellSnapshotRendersRunningWithoutTheEvent(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if started.ShellRunning {
		t.Fatalf("a freshly started orchestrator must not read a running shell before any report: %+v", started)
	}

	writeOrchestratorShellActivity(t, created.ID, orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", AtUnix: time.Now().Unix(),
	})
	app.reconcileOrchestratorActivity()

	listed := app.ListOrchestrators()
	if len(listed) != 1 || !listed[0].ShellRunning || listed[0].ShellCommand != "sleep 300" {
		t.Fatalf("expected the listed orchestrator to render the running shell from the snapshot, got %+v", listed)
	}
	info, ok := app.runningOrchestratorInfo(created.ID)
	if !ok || !info.ShellRunning || info.ShellCommand != "sleep 300" {
		t.Fatalf("expected the running snapshot to carry the shell state, got %+v (ok=%v)", info, ok)
	}
}

// TestReconcileOrchestratorActivityReEmitsShellStateEveryTick is the shell-report
// half of the busy-signal re-emit lock: the shell signal is republished every
// tick regardless of whether it changed, so a dropped or mistimed
// orchestrator-shell-activity event self-heals within one tick.
func TestReconcileOrchestratorActivityReEmitsShellStateEveryTick(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorShellActivity(t, created.ID, orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", AtUnix: time.Now().Unix(),
	})

	app.reconcileOrchestratorActivity()
	app.reconcileOrchestratorActivity()
	app.reconcileOrchestratorActivity()

	events := emits.events(orchestratorShellEvent)
	if len(events) != 3 {
		t.Fatalf("expected one emit per tick even with no state change, got %d: %+v", len(events), events)
	}
	for _, event := range events {
		payload, ok := event.(orchestratorShellActivityPayload)
		if !ok || !payload.Running || payload.Command != "sleep 300" {
			t.Fatalf("expected every re-emit to report the running shell, got %+v", event)
		}
	}
}
