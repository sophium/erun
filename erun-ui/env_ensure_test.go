package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// deploymentAbsentErrorForTest builds ensureRuntimeDeployed's exact "is not deployed" error.
func deploymentAbsentErrorForTest(tenant, environment string) error {
	return fmt.Errorf("runtime for %s/%s %s %q not found in namespace %q); run `erun deploy %s %s` first",
		tenant, environment, kubecheck.KubernetesDeploymentAbsentMessageMarker, tenant+"-devops", tenant+"-"+environment, tenant, environment)
}

func envEnsureTestApp(t *testing.T, ensures *int32, ensuresMu *sync.Mutex) *App {
	t.Helper()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		reconnectMCP: func(context.Context, config.OpenResult, func(string)) error {
			ensuresMu.Lock()
			*ensures++
			ensuresMu.Unlock()
			return nil
		},
	})
	// A non-nil ctx enables the ensure; the emitter must be stubbed or emits hit the real Wails runtime.
	app.ctx = context.Background()
	app.SetEmitter(func(string, ...any) {})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

func waitForEnsureCount(t *testing.T, ensures *int32, ensuresMu *sync.Mutex, want int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ensuresMu.Lock()
		got := *ensures
		ensuresMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	ensuresMu.Lock()
	got := *ensures
	ensuresMu.Unlock()
	t.Fatalf("ensure count = %d, want %d", got, want)
}

func TestEnsureEnvRuntimeOnceDedupesAcrossTabs(t *testing.T) {
	var ensures int32
	var ensuresMu sync.Mutex
	app := envEnsureTestApp(t, &ensures, &ensuresMu)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if _, err := app.StartAISession(selection, 0, 80, 24, false); err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)

	time.Sleep(100 * time.Millisecond)
	ensuresMu.Lock()
	got := ensures
	ensuresMu.Unlock()
	if got != 1 {
		t.Fatalf("expected the burst to share one ensure, got %d", got)
	}
}

func TestEnsureEnvRuntimeOnceRunsAgainAfterTheWindow(t *testing.T) {
	var ensures int32
	var ensuresMu sync.Mutex
	app := envEnsureTestApp(t, &ensures, &ensuresMu)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)

	// Age the window out by hand instead of sleeping out the TTL.
	key := selectionKey(uimodel.NormalizeSelection(selection))
	app.envEnsureMu.Lock()
	app.envEnsureDone[key] = time.Now().Add(-envEnsureTTL)
	app.envEnsureMu.Unlock()

	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 2, 2*time.Second)
}

func TestEnsureFailureNotificationDedupesPerEpisode(t *testing.T) {
	emits := newCapturedEmits()
	var ensures int32
	var ensuresMu sync.Mutex
	var reconnectMu sync.Mutex
	reconnectErr := errors.New("timed out waiting for API port-forward")

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		reconnectMCP: func(context.Context, config.OpenResult, func(string)) error {
			ensuresMu.Lock()
			ensures++
			ensuresMu.Unlock()
			reconnectMu.Lock()
			defer reconnectMu.Unlock()
			return reconnectErr
		},
	})
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	t.Cleanup(func() { app.shutdown(context.Background()) })

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	key := selectionKey(uimodel.NormalizeSelection(selection))

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom"))
	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom"))
	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 1 {
		t.Fatalf("notification emitted %d times in one failure episode, want 1", got)
	}
	if got := len(emits.events(uimodel.EnvStatusEvent)); got != 2 {
		t.Fatalf("env-status emitted %d times, want 2 (flagged on each attempt)", got)
	}

	reconnectMu.Lock()
	reconnectErr = nil
	reconnectMu.Unlock()
	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)
	waitForFailEpisodeCleared(t, app, key, 2*time.Second)

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom again"))
	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 2 {
		t.Fatalf("notification emitted %d times after recovery, want 2", got)
	}
}

func waitForFailEpisodeCleared(t *testing.T, app *App, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		app.envEnsureMu.Lock()
		_, still := app.envEnsureFailNotified[key]
		app.envEnsureMu.Unlock()
		if !still {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("successful ensure did not clear the failure episode")
}

// capturedEnsureApp: a nil readRunState reads as "not stopped".
func capturedEnsureApp(
	t *testing.T,
	reconnect func(context.Context, config.OpenResult, func(string)) error,
	readRunState func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	emits := newCapturedEmits()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		reconnectMCP:        reconnect,
		readRuntimeRunState: readRunState,
	})
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, emits
}

func TestSurfaceEnsureFailureSuppressedWhileDeployInFlight(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	absentErr := deploymentAbsentErrorForTest("erun", "remote")

	app.mu.Lock()
	app.sessions["s1"] = &managedTerminal{
		selection:        selection,
		key:              "s1",
		serial:           1,
		kind:             sessionKindOpen,
		lockedByActivity: "dep-1",
	}
	app.mu.Unlock()

	app.surfaceEnvRuntimeEnsureFailure(selection, absentErr)

	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 0 {
		t.Fatalf("banner emitted %d times during an in-flight deploy, want 0", got)
	}
	if got := len(emits.events(uimodel.EnvStatusEvent)); got != 0 {
		t.Fatalf("failed status emitted %d times during an in-flight deploy, want 0", got)
	}

	app.mu.Lock()
	app.sessions["s1"].lockedByActivity = ""
	app.mu.Unlock()
	app.surfaceEnvRuntimeEnsureFailure(selection, absentErr)
	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("banner emitted %d times once the deploy cleared, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if note.Kind != "warning" || note.Source != notificationSourceRuntimeUnreachable {
		t.Fatalf("notification = %+v, want kind=warning source=%q", note, notificationSourceRuntimeUnreachable)
	}
	if note.Action != notificationActionDeploy {
		t.Fatalf("notification action = %q, want %q", note.Action, notificationActionDeploy)
	}
}

// `open --reconnect` refuses to start a stopped runtime; that error is the operator's own Stop.
func TestSurfaceEnsureFailureRendersAStoppedRuntimeAsStopped(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
		return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 1}, nil
	})
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("erun/remote is stopped, and a session reconnect does not start it"))

	statuses := envStatuses(emits)
	if len(statuses) != 1 || statuses[0].Status != uimodel.EnvStatusRuntimeStopped {
		t.Fatalf("env status = %+v, want a single %q", statuses, uimodel.EnvStatusRuntimeStopped)
	}
	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if !strings.Contains(note.Message, "Open it to start it again") {
		t.Fatalf("notification must name the way back, got %q", note.Message)
	}
	// Opening is the remedy and the row already offers it, so no action button.
	if note.Action != "" {
		t.Fatalf("notification action = %q, want none", note.Action)
	}
}

func TestEnsureSuccessClearsRuntimeUnreachableNotification(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	app.ensureEnvRuntimeOnce(selection)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(uimodel.AppNotificationClearEvent)) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := emits.events(uimodel.AppNotificationClearEvent)
	if len(events) == 0 {
		t.Fatal("a successful ensure did not clear the runtime-unreachable banner")
	}
	payload, ok := events[0].(uimodel.AppNotificationClearPayload)
	if !ok {
		t.Fatalf("clear payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "erun" || payload.Environment != "remote" || payload.Source != "" {
		t.Fatalf("clear payload = %+v, want erun/remote with empty (any) source", payload)
	}
}

func TestSurfaceEnsureFailureOffersDeployForGenuinelyAbsentDeployment(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	app.surfaceEnvRuntimeEnsureFailure(selection, deploymentAbsentErrorForTest("erun", "remote"))

	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if note.Action != notificationActionDeploy {
		t.Fatalf("notification action = %q, want %q for a genuinely absent deployment", note.Action, notificationActionDeploy)
	}
	if !strings.Contains(note.Message, "Deploy the environment to bring it up") {
		t.Fatalf("notification must name deploy as the fix, got %q", note.Message)
	}
}

// The kube context was unavailable; a Deploy button would roll a healthy runtime.
func TestSurfaceEnsureFailureOffersNoDeployForACheckThatCouldNotAsk(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	checkErr := errors.New(`could not determine whether deployment "frs-devops" is deployed (the configured kubernetes context was not found in kubeconfig): error: context "frs-prod" does not exist`)

	app.surfaceEnvRuntimeEnsureFailure(selection, checkErr)

	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if note.Action != "" {
		t.Fatalf("notification action = %q, want none — a could-not-ask failure must not offer deploy", note.Action)
	}
	if strings.Contains(note.Message, "Deploy the environment") {
		t.Fatalf("notification must not suggest deploy for a check that never confirmed absence, got %q", note.Message)
	}
	if !strings.Contains(note.Message, "context") {
		t.Fatalf("notification must carry the underlying cause, got %q", note.Message)
	}
}

func TestSurfaceEnsureFailureOffersNoDeployForAnUnclassifiedFailure(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, config.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("timed out waiting for API port-forward"))

	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if note.Action != "" {
		t.Fatalf("notification action = %q, want none for an unrecognized failure", note.Action)
	}
}
