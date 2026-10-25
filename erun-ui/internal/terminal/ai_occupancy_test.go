package terminal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// newAIOccupancyTestApp returns the app and a counter of launched AI PTYs.
func newAIOccupancyTestApp(t *testing.T) (*Service, *int) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	started := 0
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(params StartParams) (PTY, error) {
			started++
			return newStubTerminalSession(), nil
		},
	})
	t.Cleanup(func() { closeTestService(app) })
	return app, &started
}

func takeTestLease(t *testing.T, name string) {
	t.Helper()
	if _, err := activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      "erun",
		Environment: "remote",
		Name:        name,
	}); err != nil {
		t.Fatalf("seed lease %q: %v", name, err)
	}
}

// Without confirmation, an env held by another job's lease is reported occupied and no PTY spawns.
func TestStartAISessionReportsOccupancyInsteadOfStartingASecondAgent(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)
	takeTestLease(t, "job-fix-1201")
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	result, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	assertOccupiedUnconfirmedResult(t, result, *started)

	confirmed, err := app.StartAISession(selection, 0, 80, 24, true)
	if err != nil {
		t.Fatalf("confirmed StartAISession failed: %v", err)
	}
	assertConfirmedStartResult(t, confirmed, *started)
}

func assertOccupiedUnconfirmedResult(t *testing.T, result uimodel.StartSessionResult, started int) {
	t.Helper()
	if started != 0 {
		t.Fatalf("expected no PTY to be started while occupied and unconfirmed, got %d", started)
	}
	if result.SessionID != 0 {
		t.Fatalf("expected no session id on an occupied, unconfirmed start, got %d", result.SessionID)
	}
	if len(result.Occupancy) != 1 || result.Occupancy[0].Name != "job-fix-1201" {
		t.Fatalf("expected the held lease to be reported as the occupant, got %+v", result.Occupancy)
	}
}

func assertConfirmedStartResult(t *testing.T, result uimodel.StartSessionResult, started int) {
	t.Helper()
	if started != 1 {
		t.Fatalf("expected the confirmed retry to start exactly one PTY, got %d", started)
	}
	if result.SessionID == 0 {
		t.Fatalf("expected a session id once confirmed, got %+v", result)
	}
	if len(result.Occupancy) != 0 {
		t.Fatalf("expected no occupancy on the confirmed result, got %+v", result.Occupancy)
	}
}

func TestStartAISessionStartsNormallyWithNoHeldLease(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)

	result, err := app.StartAISession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	if *started != 1 {
		t.Fatalf("expected the unoccupied start to launch a PTY immediately, got %d", *started)
	}
	if len(result.Occupancy) != 0 {
		t.Fatalf("expected no occupancy notice, got %+v", result.Occupancy)
	}
}

// Reattaching to this desktop's own session is not a second agent.
func TestStartAISessionReuseSkipsOccupancyCheck(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	first, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("first StartAISession failed: %v", err)
	}

	// The lease is taken after the AI tab is live.
	takeTestLease(t, "job-fix-1201")

	again, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("second StartAISession failed: %v", err)
	}
	if *started != 1 {
		t.Fatalf("expected exactly one PTY across reuse, got %d", *started)
	}
	if again.SessionID != first.SessionID {
		t.Fatalf("expected the reattach to return the same session id, got %d vs %d", again.SessionID, first.SessionID)
	}
	if len(again.Occupancy) != 0 {
		t.Fatalf("expected reuse to skip the occupancy check entirely, got %+v", again.Occupancy)
	}
}

// A snapshot caught mid-rewrite must not make the occupancy check fail open.
func TestStartAISessionReportsAHeldLeaseWhenAnActivitySnapshotIsUnreadable(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)
	takeTestLease(t, "job-fix-1201")
	dir, err := activity.EnvironmentActivityDir("erun", "remote")
	if err != nil {
		t.Fatalf("activity dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.ActivityKindCLI+".json"), nil, 0o644); err != nil {
		t.Fatalf("stage a truncated snapshot: %v", err)
	}
	if _, err := app.LoadIdleStatus(uimodel.Selection{Tenant: "erun", Environment: "remote"}); err == nil {
		t.Fatal("precondition: an empty snapshot must fail the idle-status read")
	}

	result, err := app.StartAISession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	assertOccupiedUnconfirmedResult(t, result, *started)
}
