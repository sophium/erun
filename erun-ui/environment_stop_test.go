package main

import (
	"context"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Scaled-to-zero is stopped, never a failure; pods wanted but not ready stay unhealthy.

func stopTestApp(t *testing.T, emits *capturedEmits, deps erunUIDeps) *App {
	t.Helper()
	projectRoot := t.TempDir()
	deps.store = stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "cluster-local"},
		},
	}
	deps.findProjectRoot = func() (string, string, error) { return "erun", projectRoot, nil }
	deps.resolveCLIPath = func() string { return "/tmp/erun" }
	app := NewApp(deps)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	app.SetEmitter(emits.fn())
	return app
}

// `erun open` wakes a stopped env, so a respawn would undo the operator's stop.
func TestReconnectRefusedFlagsStoppedRuntimeInsteadOfFailed(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
		readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
		},
	})

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForEnvStatus(t, emits, "")

	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForEnvStatus(t, emits, uimodel.EnvStatusRuntimeStopped)
	for _, payload := range envStatuses(emits) {
		if payload.Status == uimodel.EnvStatusFailed {
			t.Fatalf("a stopped runtime must not read as a failure: %+v", envStatuses(emits))
		}
	}
	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("expected no respawn against a stopped runtime, got %d sessions", got)
	}
}
