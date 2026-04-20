package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestStateFromListResultUsesEffectiveSelection(t *testing.T) {
	state := stateFromListResult(eruncommon.ListResult{
		CurrentDirectory: eruncommon.ListCurrentDirectoryResult{
			Effective: &eruncommon.ListEffectiveTargetResult{
				Tenant:      "erun",
				Environment: "local",
			},
		},
		Tenants: []eruncommon.ListTenantResult{
			{
				Name: "erun",
				Environments: []eruncommon.ListEnvironmentResult{
					{Name: "local"},
					{Name: "remote"},
				},
			},
		},
	})

	if state.Selected == nil {
		t.Fatal("expected selected environment")
	}
	if state.Selected.Tenant != "erun" || state.Selected.Environment != "local" {
		t.Fatalf("unexpected selected environment: %+v", state.Selected)
	}
	if len(state.Tenants) != 1 || len(state.Tenants[0].Environments) != 2 {
		t.Fatalf("unexpected tenants: %+v", state.Tenants)
	}
}

func TestBuildOpenCommandQuotesExecutableAndArgs(t *testing.T) {
	got := buildOpenCommand("/Applications/ERun App/erun", "tenant a", "prod")
	want := "'/Applications/ERun App/erun' open 'tenant a' 'prod'"
	if got != want {
		t.Fatalf("unexpected open command: got %q want %q", got, want)
	}
}

func TestBuildOpenArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildOpenArgs(" erun ", " local ")
	want := []string{"open", "erun", "local"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestResolveTerminalStartDirUsesExistingPreferredDirectory(t *testing.T) {
	preferred := t.TempDir()

	if got := resolveTerminalStartDir(preferred); got != preferred {
		t.Fatalf("resolveTerminalStartDir(%q) = %q, want %q", preferred, got, preferred)
	}
}

func TestResolveTerminalStartDirFallsBackToWorkingDirectory(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	workingDir := t.TempDir()
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory failed: %v", err)
		}
	}()

	missingDir := filepath.Join(workingDir, "missing")
	got := resolveTerminalStartDir(missingDir)
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", got, err)
	}
	wantEval, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", workingDir, err)
	}
	if gotEval != wantEval {
		t.Fatalf("resolveTerminalStartDir(%q) = %q (%q), want %q (%q)", missingDir, got, gotEval, workingDir, wantEval)
	}
}

func TestStartSessionReusesExistingSessionForSelection(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", eruncommon.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}

	second, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 80, 24)
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

type stubUIStore struct {
	tenants map[string]eruncommon.TenantConfig
	envs    map[string]eruncommon.EnvConfig
}

func (s stubUIStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return eruncommon.ERunConfig{}, "", nil
}

func (s stubUIStore) LoadTenantConfig(name string) (eruncommon.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return eruncommon.TenantConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) LoadEnvConfig(tenant, environment string) (eruncommon.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return eruncommon.EnvConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) ListTenantConfigs() ([]eruncommon.TenantConfig, error) {
	return nil, nil
}

func (s stubUIStore) ListEnvConfigs(string) ([]eruncommon.EnvConfig, error) {
	return nil, nil
}

type stubTerminalSession struct {
	closeCh chan struct{}
}

func newStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{closeCh: make(chan struct{})}
}

func (s *stubTerminalSession) Read([]byte) (int, error) {
	<-s.closeCh
	return 0, io.EOF
}

func (s *stubTerminalSession) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (s *stubTerminalSession) Resize(int, int) error {
	return nil
}

func (s *stubTerminalSession) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}
