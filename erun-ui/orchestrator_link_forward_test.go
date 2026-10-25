package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func orchestratorLinkForwardTestApp(
	t *testing.T,
	reconnect func(context.Context, config.OpenResult, func(string)) error,
	readRunState func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	emits := newCapturedEmits()
	// ResolveOpen needs a resolvable repo path even for a remote-agent env.
	store := newOrchestratorStubStore(t.TempDir())
	devEnv := store.envs["frs/dev"]
	devEnv.LocalRepoPath = t.TempDir()
	store.envs["frs/dev"] = devEnv
	laptopEnv := store.envs["frs/laptop"]
	laptopEnv.KubernetesContext = "ctx"
	store.envs["frs/laptop"] = laptopEnv
	app := NewApp(erunUIDeps{
		store: store,
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			return newStubTerminalSession(), nil
		},
		reconnectMCP:        reconnect,
		readRuntimeRunState: readRunState,
		canReachMCPEndpoint: orchestratorTestAlwaysReachable,
	})
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	app.investigations.reportDir = t.TempDir()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, emits
}

func countingReconnect(mu *sync.Mutex, calls *[]string, err error) func(context.Context, config.OpenResult, func(string)) error {
	return func(_ context.Context, result config.OpenResult, _ func(string)) error {
		mu.Lock()
		*calls = append(*calls, result.Tenant+"/"+result.Environment)
		mu.Unlock()
		return err
	}
}

func waitForReconnectCalls(t *testing.T, mu *sync.Mutex, calls *[]string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*calls)
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := len(*calls)
	mu.Unlock()
	t.Fatalf("reconnect calls = %d, want >= %d", got, want)
}

func TestCreateOrchestratorLinkOpensTheForwardWithoutManualOpen(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	app, _ := orchestratorLinkForwardTestApp(t, countingReconnect(&mu, &calls, nil), nil)

	if _, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{
		{Tenant: "frs", Environment: "dev"},
	}, nil, "", orchestratorsvc.LaunchInput{}); err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	waitForReconnectCalls(t, &mu, &calls, 1, 2*time.Second)
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "frs/dev" {
		t.Fatalf("expected linking to open frs/dev's forward, got %v", got)
	}
}

func TestUpdateOrchestratorLinkOpensTheForwardForANewlyAddedEnv(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	app, _ := orchestratorLinkForwardTestApp(t, countingReconnect(&mu, &calls, nil), nil)

	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{
		{Tenant: "frs", Environment: "dev"},
	}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	waitForReconnectCalls(t, &mu, &calls, 1, 2*time.Second)

	if _, err := app.UpdateOrchestrator(created.ID, "agent", []orchestratorsvc.EnvInput{
		{Tenant: "frs", Environment: "dev"},
		{Tenant: "frs", Environment: "laptop"},
	}, nil, "", orchestratorsvc.LaunchInput{}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	waitForReconnectCalls(t, &mu, &calls, 2, 2*time.Second)
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	found := false
	for _, call := range got {
		if call == "frs/laptop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected relinking to open the newly added frs/laptop forward too, got %v", got)
	}
}

func TestLinkingAStoppedEnvironmentDoesNotForceStartIt(t *testing.T) {
	app, emits := orchestratorLinkForwardTestApp(t,
		func(context.Context, config.OpenResult, func(string)) error {
			return errors.New("frs/dev is stopped, and a session reconnect does not start it; run `erun open frs dev` to start it again")
		},
		func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0}, nil
		},
	)

	if _, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{
		{Tenant: "frs", Environment: "dev"},
	}, nil, "", orchestratorsvc.LaunchInput{}); err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	waitForEnvStatus(t, emits, uimodel.EnvStatusRuntimeStopped)
	for _, status := range envStatuses(emits) {
		if status.Status == uimodel.EnvStatusFailed {
			t.Fatalf("a stopped environment must never surface as failed, got %+v", status)
		}
	}
}

func TestLinkingAnEnvironmentSurfacesAForwardOpenFailure(t *testing.T) {
	app, emits := orchestratorLinkForwardTestApp(t,
		func(context.Context, config.OpenResult, func(string)) error {
			return errors.New("timed out waiting for MCP port-forward")
		},
		nil,
	)

	if _, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{
		{Tenant: "frs", Environment: "dev"},
	}, nil, "", orchestratorsvc.LaunchInput{}); err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	waitForEnvStatus(t, emits, uimodel.EnvStatusFailed)
	posted := emits.waitFor(envStatusWaitBound, func(byName map[string][]any) bool {
		return len(byName[uimodel.AppNotificationEvent]) > 0
	})
	if !posted {
		t.Fatal("a forward-open failure must post a visible notification, got none")
	}
}
