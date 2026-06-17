package main

import (
	"context"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These tests lock the per-env ensure dedupe (issue #463): opening an env
// with its default tabs — and respawning them — must run the open/build/
// deploy preflight once per (re)start window, not once per tab. The tabs'
// own `erun open` processes launch with --skip-ensure (locked by the
// StartSession/StartAISession arg assertions in app_test.go) and the shared
// ensure is the only preflight runner.

func envEnsureTestApp(t *testing.T, ensures *int32, ensuresMu *sync.Mutex) *App {
	t.Helper()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
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
	// The ensure no-ops while a.ctx is nil (the unit-test hermeticity gate);
	// these tests inject every dep it reaches, so flipping the gate is safe.
	// The emitter must be stubbed alongside it: with a non-nil ctx, emits
	// would otherwise reach the real Wails runtime, which fatals outside a
	// lifecycle context.
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
	if _, err := app.StartAISession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	// Both tab spawns share one preflight.
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

	// Age the completed window out (white-box: the TTL constant is the
	// contract; sleeping it out would slow the suite for nothing).
	key := selectionKey(normalizeSelection(selection))
	app.envEnsureMu.Lock()
	app.envEnsureDone[key] = time.Now().Add(-envEnsureTTL)
	app.envEnsureMu.Unlock()

	app.ensureEnvRuntimeOnce(selection)
	waitForEnsureCount(t, &ensures, &ensuresMu, 2, 2*time.Second)
}
