package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorLinkForwardTestApp wires the same frs/dev remote-agent env
// newOrchestratorStubStore uses, plus an injectable reconnectMCP/
// readRuntimeRunState seam, so a test can observe whether linking an
// environment triggered ensureEnvRuntimeOnce's underlying `erun open
// --reconnect` and how its outcome surfaced -- without a live cluster.
func orchestratorLinkForwardTestApp(
	t *testing.T,
	reconnect func(context.Context, eruncommon.OpenResult, func(string)) error,
	readRunState func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	emits := newCapturedEmits()
	// ResolveOpen (unlike the review-directory/MCP-port wiring the other
	// orchestrator tests exercise) requires a resolvable repo path even for a
	// remote-agent env, so frs/dev needs a LocalRepoPath here that
	// newOrchestratorStubStore's default does not set.
	store := newOrchestratorStubStore(t.TempDir())
	devEnv := store.envs["frs/dev"]
	devEnv.LocalRepoPath = t.TempDir()
	store.envs["frs/dev"] = devEnv
	laptopEnv := store.envs["frs/laptop"]
	laptopEnv.KubernetesContext = "ctx"
	store.envs["frs/laptop"] = laptopEnv
	app := NewApp(erunUIDeps{
		store: store,
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
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

// countingReconnect returns a reconnectMCP stub that records every call
// ("<tenant>/<environment>") and answers with err, so a test can assert both
// that the ensure ran and what its outcome was.
func countingReconnect(mu *sync.Mutex, calls *[]string, err error) func(context.Context, eruncommon.OpenResult, func(string)) error {
	return func(_ context.Context, result eruncommon.OpenResult, _ func(string)) error {
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

// TestCreateOrchestratorLinkOpensTheForwardWithoutManualOpen locks the fix:
// linking a healthy environment to an orchestrator must make it reachable on
// its own -- the operator is never told a manual `erun open` is the reason
// their tools work. Before the fix, CreateOrchestrator wired the review
// directory and nothing else, so an environment nobody had opened by hand
// stayed unreachable for the whole session.
func TestCreateOrchestratorLinkOpensTheForwardWithoutManualOpen(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	app, _ := orchestratorLinkForwardTestApp(t, countingReconnect(&mu, &calls, nil), nil)

	if _, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "dev"},
	}); err != nil {
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

// TestUpdateOrchestratorLinkOpensTheForwardForANewlyAddedEnv is the
// UpdateOrchestrator half of the same fix: relinking an orchestrator to add a
// new environment must open that environment's forward too, not only the
// ones it started with.
func TestUpdateOrchestratorLinkOpensTheForwardForANewlyAddedEnv(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	app, _ := orchestratorLinkForwardTestApp(t, countingReconnect(&mu, &calls, nil), nil)

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "dev"},
	})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	waitForReconnectCalls(t, &mu, &calls, 1, 2*time.Second)

	if _, err := app.UpdateOrchestrator(created.ID, "agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "dev"},
		{Tenant: "frs", Environment: "laptop"},
	}); err != nil {
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

// TestLinkingAStoppedEnvironmentDoesNotForceStartIt locks the second
// acceptance criterion: a stopped environment must be left stopped, not
// silently started because it got linked to an orchestrator. The forward
// ensure runs `erun open --reconnect`, which refuses to wake a stopped
// runtime by design (erun-common/stop.go's DecideRuntimeWake); linking must
// inherit that refusal rather than bypass it, and the operator must see the
// environment reported as stopped, not failed or running.
func TestLinkingAStoppedEnvironmentDoesNotForceStartIt(t *testing.T) {
	app, emits := orchestratorLinkForwardTestApp(t,
		func(context.Context, eruncommon.OpenResult, func(string)) error {
			return errors.New("frs/dev is stopped, and a session reconnect does not start it; run `erun open frs dev` to start it again")
		},
		func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0}, nil
		},
	)

	if _, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "dev"},
	}); err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	waitForEnvStatus(t, emits, envStatusRuntimeStopped)
	for _, status := range envStatuses(emits) {
		if status.Status == envStatusFailed {
			t.Fatalf("a stopped environment must never surface as failed, got %+v", status)
		}
	}
}

// TestLinkingAnEnvironmentSurfacesAForwardOpenFailure locks the third
// acceptance criterion: if opening the forward fails for a reason other than
// "stopped" or "genuinely undeployed", the failure must be visible on the
// sidebar and as a notification -- never silent, leaving a configured MCP
// entry pointed at nothing with no clue why.
func TestLinkingAnEnvironmentSurfacesAForwardOpenFailure(t *testing.T) {
	app, emits := orchestratorLinkForwardTestApp(t,
		func(context.Context, eruncommon.OpenResult, func(string)) error {
			return errors.New("timed out waiting for MCP port-forward")
		},
		nil,
	)

	if _, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "dev"},
	}); err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	waitForEnvStatus(t, emits, envStatusFailed)
	posted := emits.waitFor(envStatusWaitBound, func(byName map[string][]any) bool {
		return len(byName[appNotificationEvent]) > 0
	})
	if !posted {
		t.Fatal("a forward-open failure must post a visible notification, got none")
	}
}
