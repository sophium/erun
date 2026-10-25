package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

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
			selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
			managed := &managedTerminal{
				kind:       sessionKindAI,
				selection:  selection,
				key:        "ai\x00petios\x00local",
				serial:     3,
				appSession: "ai",
				// Busy and silent past the idle threshold.
				aiBusyEmitted: true,
				aiLastOutput:  time.Now().Add(-time.Minute),
			}
			app.sessions[managed.key] = managed
			app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(tc.podRunning)

			app.clearAIActivityIfQuiet(managed)

			cleared := len(emits.events(uimodel.AIActivityEvent)) > 0
			if cleared != tc.wantBusyClear {
				t.Fatalf("busy cleared = %v, want %v (events: %+v)", cleared, tc.wantBusyClear, emits.events(uimodel.AIActivityEvent))
			}
			if managed.aiBusyEmitted == tc.wantBusyClear {
				t.Fatalf("latch state %v contradicts the emitted signal", managed.aiBusyEmitted)
			}
		})
	}
}

func TestHeartbeatReleasesBusyWhenThePodReportsTheSessionFinished(t *testing.T) {
	emits := newCapturedEmits()
	selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
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
		aiLastOutput:  time.Now(),
	}
	app.sessions[managed.key] = managed

	app.applySessionHeartbeat(selection, uiRuntimeActivity{
		Sessions:        []uiRuntimeSession{{ID: "ai", Running: true, Program: "claude"}},
		SessionsRunning: 1,
	})
	if len(emits.events(uimodel.AIActivityEvent)) != 0 {
		t.Fatalf("a running session must not be released: %+v", emits.events(uimodel.AIActivityEvent))
	}

	app.applySessionHeartbeat(selection, uiRuntimeActivity{
		Sessions:        []uiRuntimeSession{{ID: "ai", Running: false}},
		SessionsRunning: 0,
	})
	if len(emits.events(uimodel.AIActivityEvent)) != 1 {
		t.Fatalf("a finished session must be released once: %+v", emits.events(uimodel.AIActivityEvent))
	}
	if managed.aiBusyEmitted {
		t.Fatalf("latch must be down after the pod reports the program gone")
	}
}

func TestHeartbeatObservationExpires(t *testing.T) {
	selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
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

func TestRuntimeActivityCountMatchesRunningSessions(t *testing.T) {
	probe := "erun-session\tai\t900\tclaude\n" +
		"erun-session\topen-0\t901\tbash\n" +
		"erun-session\topen-3\t0\t\n"

	activity := runtimeActivityFromProbe(uimodel.Selection{Tenant: "petios", Environment: "local"}, probe)
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

func TestSelectionsWithPodSessionsSkipsSessionsWithoutAPodSession(t *testing.T) {
	app := &App{sessions: make(map[string]*managedTerminal)}
	app.sessions["local"] = &managedTerminal{kind: sessionKindLocal, selection: uimodel.Selection{Tenant: "petios", Environment: "local"}}
	app.sessions["ai"] = &managedTerminal{kind: sessionKindAI, appSession: "ai", selection: uimodel.Selection{Tenant: "petios", Environment: "remote"}}
	app.sessions["open"] = &managedTerminal{kind: sessionKindOpen, appSession: "open-0", selection: uimodel.Selection{Tenant: "petios", Environment: "remote"}}

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

func TestReclaimRuntimeResourcesRunsTheNamedActionOnly(t *testing.T) {
	var ran []string
	app := NewApp(erunUIDeps{
		execRuntimePod: func(_ context.Context, _ uimodel.Selection, script string) (string, error) {
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

// Deliberately ignores emitted events: a remount only reads the snapshot.
func TestOrchestratorSnapshotRendersBusyWithoutTheEvent(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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

func TestReconcileOrchestratorActivityReEmitsEveryTick(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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

	events := emits.events(uimodel.AIActivityEvent)
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

func TestOrchestratorShellSnapshotRendersRunningWithoutTheEvent(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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

// Orchestrator ids are reused across restarts, so a shell report must name its own session.
func TestOrchestratorShellActivityDoesNotBorrowASuccessorSessionsLiveness(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorShellActivity(t, created.ID, orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", SessionID: "dead-session", AtUnix: time.Now().Unix(),
	})

	app.reconcileOrchestratorActivity()

	listed := app.ListOrchestrators()
	if len(listed) != 1 || listed[0].ShellRunning {
		t.Fatalf("a report from a replaced session must not keep the indicator lit, got %+v", listed)
	}
}

func TestReconcileOrchestratorActivityReEmitsShellStateEveryTick(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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

	events := emits.events(uimodel.OrchestratorShellEvent)
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
