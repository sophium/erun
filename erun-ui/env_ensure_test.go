package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

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
