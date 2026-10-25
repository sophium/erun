package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestStartSessionLeavesCloudContextStartupToErunCommand(t *testing.T) {
	projectRoot := t.TempDir()
	rootConfig := &configpkg.ERunConfig{
		CloudProviders: []model.CloudProviderConfig{
			{Alias: "team-cloud", Provider: configpkg.CloudProviderAWS},
		},
		CloudContexts: []model.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           configpkg.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             cloud.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       configpkg.DefaultCloudContextInstanceType,
				DiskType:           configpkg.DefaultCloudContextDiskType,
				DiskSizeGB:         configpkg.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-prod",
				AdminToken:         "test-token",
			},
		},
	}
	var actions []string
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			config: rootConfig,
			tenants: map[string]model.TenantConfig{
				"frs": {Name: "frs", DefaultEnvironment: "prod"},
			},
			envs: map[string]configpkg.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-prod",
					Type:              configpkg.EnvironmentTypeRuntime,
				},
			},
		},
		resolveCLIPath:   func() string { return "/tmp/erun" },
		cloudContextDeps: testCloudContextDeps(&actions),
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			actions = append(actions, "terminal "+strings.Join(params.Args, " "))
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uimodel.Selection{Tenant: "frs", Environment: "prod"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	got := strings.Join(actions, "\n")
	if got != "terminal open frs prod --app-session open-0" {
		t.Fatalf("expected only terminal start action, got:\n%s", got)
	}
	// Cloud-context Status is not persisted; the action log above proves no AWS start ran.
}

func TestStartSessionAllocatesIndependentSlots(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", configpkg.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}
	second, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 1, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}
	reuse, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("third StartSession failed: %v", err)
	}

	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
	if first.SessionID == second.SessionID {
		t.Fatalf("expected distinct session ids for different slots, got %d for both", first.SessionID)
	}
	if reuse.SessionID != first.SessionID {
		t.Fatalf("expected slot 0 to reuse session %d, got %d", first.SessionID, reuse.SessionID)
	}
	if first.Slot != 0 || second.Slot != 1 {
		t.Fatalf("unexpected slot values: first=%d second=%d", first.Slot, second.Slot)
	}
}

func TestSendSessionInputRoutesPerSession(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var stubs []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", configpkg.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			stubs = append(stubs, s)
			return s, nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}
	second, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 1, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}

	if err := app.SendSessionInput(first.SessionID, "alpha"); err != nil {
		t.Fatalf("SendSessionInput first failed: %v", err)
	}
	if err := app.SendSessionInput(second.SessionID, "beta"); err != nil {
		t.Fatalf("SendSessionInput second failed: %v", err)
	}

	if len(stubs) != 2 {
		t.Fatalf("expected two terminal stubs, got %d", len(stubs))
	}
	if got := stubs[0].WrittenString(); got != "alpha" {
		t.Fatalf("slot 0 received %q, want %q", got, "alpha")
	}
	if got := stubs[1].WrittenString(); got != "beta" {
		t.Fatalf("slot 1 received %q, want %q", got, "beta")
	}
}

func TestStartSessionReusesExistingSessionForSelection(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", configpkg.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}

	second, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}

	if startCalls != 1 {
		t.Fatalf("start terminal called %d times, want 1", startCalls)
	}
	if first.SessionID != second.SessionID {
		t.Fatalf("session ids differ: first=%d second=%d", first.SessionID, second.SessionID)
	}
}

func TestSendSessionInputRecordsCLIActivityForCurrentEnvironment(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	var recordMu sync.Mutex
	var recorded []model.EnvironmentActivityParams
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			// Silent: the 2s activity ticker fires on the first chunk and would race the assertion.
			return newSilentStubTerminalSession(), nil
		},
		recordActivity: func(params model.EnvironmentActivityParams) error {
			recordMu.Lock()
			defer recordMu.Unlock()
			recorded = append(recorded, params)
			return nil
		},
	})
	defer app.shutdown(context.Background())

	started, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if err := app.SendSessionInput(started.SessionID, "date\r"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}

	recordMu.Lock()
	defer recordMu.Unlock()
	if len(recorded) != 2 {
		t.Fatalf("recorded %d activity events, want 2 (session start and input): %+v", len(recorded), recorded)
	}
	for _, event := range recorded {
		if event.Tenant != "erun" || event.Environment != "local" || event.Kind != configpkg.ActivityKindCLI {
			t.Fatalf("unexpected activity params: %+v", event)
		}
	}
}

func TestSendSessionInputClearsAwaitingPostRespawnInputFlag(t *testing.T) {
	// Input after a respawn must re-enable output-driven idle refreshes.
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		recordActivity: func(model.EnvironmentActivityParams) error { return nil },
	})
	defer app.shutdown(context.Background())

	started, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	app.mu.Lock()
	managed := app.sessionBySerialLocked(started.SessionID)
	if managed == nil {
		app.mu.Unlock()
		t.Fatalf("expected managed terminal for session %d", started.SessionID)
	}
	managed.awaitingPostRespawnInput = true
	app.mu.Unlock()

	if !app.isAwaitingPostRespawnInput(managed) {
		t.Fatal("expected flag to read true after manual set")
	}
	if err := app.SendSessionInput(started.SessionID, "ls\r"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}
	if app.isAwaitingPostRespawnInput(managed) {
		t.Fatal("expected SendSessionInput to clear awaitingPostRespawnInput flag")
	}
}
