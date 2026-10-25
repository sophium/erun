package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Deliberately ignores emitted events: a remount only reads the snapshot.
func TestOrchestratorSnapshotRendersBusyWithoutTheEvent(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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
	app.ReconcileOrchestratorActivity()

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

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorActivity(t, created.ID, orchestratorActivity{Busy: true, AtUnix: time.Now().Unix()})

	app.ReconcileOrchestratorActivity()
	app.ReconcileOrchestratorActivity()
	app.ReconcileOrchestratorActivity()

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

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
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
	app.ReconcileOrchestratorActivity()

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

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorShellActivity(t, created.ID, orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", SessionID: "dead-session", AtUnix: time.Now().Unix(),
	})

	app.ReconcileOrchestratorActivity()

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

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	writeOrchestratorShellActivity(t, created.ID, orchestratorShellActivity{
		Running: true, Command: "sleep 300", TaskID: "task-1", AtUnix: time.Now().Unix(),
	})

	app.ReconcileOrchestratorActivity()
	app.ReconcileOrchestratorActivity()
	app.ReconcileOrchestratorActivity()

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
