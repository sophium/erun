package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These tests lock the per-env ensure dedupe: opening an env and respawning
// its tabs must run the shared ensure once per (re)start window, not once per tab.

func envEnsureTestApp(t *testing.T, ensures *int32, ensuresMu *sync.Mutex) *App {
	t.Helper()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
		reconnectMCP: func(context.Context, eruncommon.OpenResult, func(string)) error {
			ensuresMu.Lock()
			*ensures++
			ensuresMu.Unlock()
			return nil
		},
	})
	// A non-nil ctx un-gates the ensure (it no-ops while ctx is nil); the emitter
	// must be stubbed alongside it, or emits reach the real Wails runtime and fatal
	// outside a lifecycle context.
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
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if _, err := app.StartAISession(selection, 0, 80, 24, false); err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)

	// Still one after a settling beat — the AI spawn must not have queued a
	// second ensure behind the first.
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
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)

	// Age the completed window out by hand instead of sleeping out the TTL.
	key := selectionKey(normalizeSelection(selection))
	app.envEnsureMu.Lock()
	app.envEnsureDone[key] = time.Now().Add(-envEnsureTTL)
	app.envEnsureMu.Unlock()

	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 2, 2*time.Second)
}

// TestEnsureFailureNotificationDedupesPerEpisode locks the fix: a runtime
// that stays unreachable must surface its "Could not reach the runtime"
// notification once per failure episode, not on every ensure retry — otherwise
// the banner re-appears the instant the user dismisses it. The sidebar row is
// still flagged failed on every attempt, and reaching the env again ends the
// episode so a later failure surfaces afresh.
func TestEnsureFailureNotificationDedupesPerEpisode(t *testing.T) {
	emits := newCapturedEmits()
	var ensures int32
	var ensuresMu sync.Mutex
	var reconnectMu sync.Mutex
	reconnectErr := error(errors.New("timed out waiting for API port-forward"))

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
		reconnectMCP: func(context.Context, eruncommon.OpenResult, func(string)) error {
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

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	key := selectionKey(normalizeSelection(selection))

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom"))
	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom"))
	if got := len(emits.events(appNotificationEvent)); got != 1 {
		t.Fatalf("notification emitted %d times in one failure episode, want 1", got)
	}
	if got := len(emits.events(envStatusEvent)); got != 2 {
		t.Fatalf("env-status emitted %d times, want 2 (flagged on each attempt)", got)
	}

	reconnectMu.Lock()
	reconnectErr = nil
	reconnectMu.Unlock()
	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 1, 2*time.Second)
	waitForFailEpisodeCleared(t, app, key, 2*time.Second)

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("boom again"))
	if got := len(emits.events(appNotificationEvent)); got != 2 {
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

// capturedEnsureApp builds an ensure-focused app. readRunState is optional; a
// nil one leaves the cluster unreadable, which the stopped check reads as "not
// stopped" so a failure surfaces as a failure.
func capturedEnsureApp(
	t *testing.T,
	reconnect func(context.Context, eruncommon.OpenResult, func(string)) error,
	readRunState func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	emits := newCapturedEmits()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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

// TestSurfaceEnsureFailureSuppressedWhileDeployInFlight locks the fix: a
// deploy for the env being in flight IS the recovery the runtime-unreachable
// banner would recommend ("Deploy the environment …"), and the deploy-progress
// overlay already communicates it. Surfacing a contradictory failed status +
// banner on top of the running deploy is the confusion this fixes, so the
// failure must stay quiet while a deploy locks the env's terminals.
func TestSurfaceEnsureFailureSuppressedWhileDeployInFlight(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, eruncommon.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	app.mu.Lock()
	app.sessions["s1"] = &managedTerminal{
		selection:        selection,
		key:              "s1",
		serial:           1,
		kind:             sessionKindOpen,
		lockedByActivity: "dep-1",
	}
	app.mu.Unlock()

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("timed out waiting for API port-forward"))

	if got := len(emits.events(appNotificationEvent)); got != 0 {
		t.Fatalf("banner emitted %d times during an in-flight deploy, want 0", got)
	}
	if got := len(emits.events(envStatusEvent)); got != 0 {
		t.Fatalf("failed status emitted %d times during an in-flight deploy, want 0", got)
	}

	app.mu.Lock()
	app.sessions["s1"].lockedByActivity = ""
	app.mu.Unlock()
	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("timed out waiting for API port-forward"))
	notes := emits.events(appNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("banner emitted %d times once the deploy cleared, want 1", len(notes))
	}
	note, ok := notes[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	// Kind "warning" (not "warn") is the contract the frontend maps to the
	// attention icon; an unrecognized kind renders as a neutral info ⓘ.
	if note.Kind != "warning" || note.Source != notificationSourceRuntimeUnreachable {
		t.Fatalf("notification = %+v, want kind=warning source=%q", note, notificationSourceRuntimeUnreachable)
	}
}

// TestSurfaceEnsureFailureRendersAStoppedRuntimeAsStopped locks the outcome an
// operator sees after stopping an env with tabs open. The forwarder rebind runs
// `erun open --reconnect`, which refuses to start a stopped runtime by design,
// so its error is the operator's own Stop coming back — rendering it as failed
// would report their command as an outage and offer a deploy that is not the
// recovery. The row reads stopped and the banner names opening as the way back.
func TestSurfaceEnsureFailureRendersAStoppedRuntimeAsStopped(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, eruncommon.OpenResult, func(string)) error {
		return nil
	}, func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
		return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 1}, nil
	})
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	app.surfaceEnvRuntimeEnsureFailure(selection, errors.New("erun/remote is stopped, and a session reconnect does not start it"))

	statuses := envStatuses(emits)
	if len(statuses) != 1 || statuses[0].Status != envStatusRuntimeStopped {
		t.Fatalf("env status = %+v, want a single %q", statuses, envStatusRuntimeStopped)
	}
	notes := emits.events(appNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	note, ok := notes[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if !strings.Contains(note.Message, "Open it to start it again") {
		t.Fatalf("notification must name the way back, got %q", note.Message)
	}
}

// TestEnsureSuccessClearsRuntimeUnreachableNotification locks the fix: once
// the runtime is reachable again, the "Could not reach the runtime …" banner is
// stale, so a successful ensure clears it (tagged by env + source so an
// unrelated toast is left alone).
func TestEnsureSuccessClearsRuntimeUnreachableNotification(t *testing.T) {
	app, emits := capturedEnsureApp(t, func(context.Context, eruncommon.OpenResult, func(string)) error {
		return nil
	}, nil)
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	app.ensureEnvRuntimeOnce(selection)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(appNotificationClearEvent)) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := emits.events(appNotificationClearEvent)
	if len(events) == 0 {
		t.Fatal("a successful ensure did not clear the runtime-unreachable banner")
	}
	payload, ok := events[0].(appNotificationClearPayload)
	if !ok {
		t.Fatalf("clear payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "erun" || payload.Environment != "remote" || payload.Source != "" {
		t.Fatalf("clear payload = %+v, want erun/remote with empty (any) source", payload)
	}
}
