package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/internal"
)

func setupXDGConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
	return dir
}

func TestRunLoadsExistingConfiguration(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "ignored", "/ignored", nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(InitRequest{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.CreatedERunConfig || result.CreatedTenantConfig || result.CreatedEnvConfig {
		t.Fatalf("expected existing config to be reused, got %+v", result)
	}
	if result.TenantConfig.ProjectRoot != projectRoot {
		t.Fatalf("unexpected project root: %s", result.TenantConfig.ProjectRoot)
	}
}

func TestRunRespectsExistingTenantDefaultEnvironment(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{Name: "prod", RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(InitRequest{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Name != "prod" {
		t.Fatalf("expected tenant default environment to be used, got %+v", result.EnvConfig)
	}
}

func TestRunResolveTenantUsesCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	setupXDGConfigHome(t)

	projectRootA := filepath.Join(t.TempDir(), "tenant-a")
	projectRootB := filepath.Join(t.TempDir(), "tenant-b")
	for _, tenant := range []struct {
		name        string
		projectRoot string
	}{
		{name: "tenant-a", projectRoot: projectRootA},
		{name: "tenant-b", projectRoot: projectRootB},
	} {
		if err := internal.SaveTenantConfig(internal.TenantConfig{
			Name:               tenant.name,
			ProjectRoot:        tenant.projectRoot,
			DefaultEnvironment: DefaultEnvironment,
		}); err != nil {
			t.Fatalf("save tenant config: %v", err)
		}
		if err := internal.SaveEnvConfig(tenant.name, internal.EnvConfig{Name: DefaultEnvironment, RepoPath: tenant.projectRoot}); err != nil {
			t.Fatalf("save env config: %v", err)
		}
	}
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-b"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return filepath.Join(projectRootA, "nested"), nil
		},
		SelectTenant: func([]internal.TenantConfig) (TenantSelectionResult, error) {
			t.Fatal("unexpected tenant selection")
			return TenantSelectionResult{}, nil
		},
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(InitRequest{ResolveTenant: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TenantConfig.Name != "tenant-a" {
		t.Fatalf("expected current directory tenant to win, got %+v", result.TenantConfig)
	}
}

func TestRunResolveTenantSelectsConfiguredTenantWhenOutsideTenantDirectory(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return t.TempDir(), nil
		},
		SelectTenant: func(tenants []internal.TenantConfig) (TenantSelectionResult, error) {
			if len(tenants) != 1 || tenants[0].Name != "tenant-a" {
				t.Fatalf("unexpected tenant options: %+v", tenants)
			}
			return TenantSelectionResult{Tenant: "tenant-a"}, nil
		},
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		Confirm: func(label string) (bool, error) {
			t.Fatalf("unexpected confirmation: %s", label)
			return false, nil
		},
	}

	result, err := service.Run(InitRequest{ResolveTenant: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !result.CreatedERunConfig || result.CreatedTenantConfig || result.CreatedEnvConfig {
		t.Fatalf("unexpected init result: %+v", result)
	}
	if result.TenantConfig.Name != "tenant-a" {
		t.Fatalf("unexpected tenant config: %+v", result.TenantConfig)
	}
}

func TestRunResolveTenantCanInitializeCurrentProjectFromSelection(t *testing.T) {
	setupXDGConfigHome(t)

	existingProjectRoot := filepath.Join(t.TempDir(), "tenant-b")
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-b",
		ProjectRoot:        existingProjectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-b", internal.EnvConfig{Name: DefaultEnvironment, RepoPath: existingProjectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	projectRoot := filepath.Join(t.TempDir(), "project")
	service := Service{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return t.TempDir(), nil
		},
		SelectTenant: func([]internal.TenantConfig) (TenantSelectionResult, error) {
			return TenantSelectionResult{Initialize: true}, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
	}

	result, err := service.Run(InitRequest{ResolveTenant: true, AutoApprove: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !result.CreatedERunConfig || !result.CreatedTenantConfig || !result.CreatedEnvConfig {
		t.Fatalf("expected selection to initialize current project, got %+v", result)
	}
	if result.TenantConfig.Name != "tenant-a" || result.TenantConfig.ProjectRoot != projectRoot {
		t.Fatalf("unexpected tenant config: %+v", result.TenantConfig)
	}
	if result.EnvConfig.RepoPath != projectRoot {
		t.Fatalf("unexpected env config: %+v", result.EnvConfig)
	}
}

func TestRunStoresConfiguredEnvironmentBranch(t *testing.T) {
	setupXDGConfigHome(t)

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
	}

	result, err := service.Run(InitRequest{AutoApprove: true, Branch: "develop"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Branch != "develop" {
		t.Fatalf("expected branch to be saved, got %+v", result.EnvConfig)
	}

	loaded, _, err := internal.LoadEnvConfig("tenant-a", DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if loaded.Branch != "develop" {
		t.Fatalf("expected persisted branch, got %+v", loaded)
	}
}

func TestRunDetectsCurrentBranchForNewEnvironment(t *testing.T) {
	setupXDGConfigHome(t)

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
		FindCurrentBranch: func(string) (string, error) {
			return "develop", nil
		},
	}

	result, err := service.Run(InitRequest{AutoApprove: true, DetectEnvironmentBranch: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Branch != "develop" {
		t.Fatalf("expected detected branch to be saved, got %+v", result.EnvConfig)
	}
}

func TestRunPreservesExistingEnvironmentBranchWhenFlagOmitted(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{
		Name:     DefaultEnvironment,
		RepoPath: projectRoot,
		Branch:   "release",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
	}

	result, err := service.Run(InitRequest{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Branch != "release" {
		t.Fatalf("expected branch to remain unchanged, got %+v", result.EnvConfig)
	}
}

func TestRunUpdatesExistingEnvironmentBranchWhenFlagProvided(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{
		Name:     DefaultEnvironment,
		RepoPath: projectRoot,
		Branch:   "release",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, projectRoot, nil
		},
	}

	result, err := service.Run(InitRequest{Branch: "develop"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.EnvConfig.Branch != "develop" {
		t.Fatalf("expected branch to be updated, got %+v", result.EnvConfig)
	}

	loaded, _, err := internal.LoadEnvConfig(tenant, DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if loaded.Branch != "develop" {
		t.Fatalf("expected persisted branch update, got %+v", loaded)
	}
}

func TestRunResolveTenantSelectionCancelled(t *testing.T) {
	setupXDGConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: DefaultEnvironment, RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		GetWorkingDir: func() (string, error) {
			return t.TempDir(), nil
		},
		SelectTenant: func([]internal.TenantConfig) (TenantSelectionResult, error) {
			return TenantSelectionResult{}, nil
		},
	}

	if _, err := service.Run(InitRequest{ResolveTenant: true}); !errors.Is(err, ErrTenantSelectionCancelled) {
		t.Fatalf("expected ErrTenantSelectionCancelled, got %v", err)
	}
}

func TestRunBootstrapsNewProjectWithAutoApprove(t *testing.T) {
	setupXDGConfigHome(t)

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
	}

	result, err := service.Run(InitRequest{AutoApprove: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !result.CreatedERunConfig || !result.CreatedTenantConfig || !result.CreatedEnvConfig {
		t.Fatalf("expected all configs to be created, got %+v", result)
	}
	if result.TenantConfig.Name != "tenant-a" || result.EnvConfig.Name != DefaultEnvironment || result.EnvConfig.RepoPath != "/tmp/project" {
		t.Fatalf("unexpected init result: %+v", result)
	}
}

func TestRunFailsOutsideGitRepository(t *testing.T) {
	setupXDGConfigHome(t)

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	}

	if _, err := service.Run(InitRequest{AutoApprove: true}); !errors.Is(err, internal.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}

func TestRunLogsProgress(t *testing.T) {
	setupXDGConfigHome(t)

	logger := &testLogger{}
	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
		Logger: logger,
	}

	if _, err := service.Run(InitRequest{AutoApprove: true}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, want := range []string{
		"Loading erun tool configuration",
		"Trying to detect current project directory",
		"Saving default config",
		"Loaded erun tool configuration",
		"Loading tenant configuration",
		"Adding new tenant",
		"Loaded tenant configuration",
		"Loading environment configuration",
		"Adding new environment",
		"Configuration initialized OK",
	} {
		if !logger.containsTrace(want) {
			t.Fatalf("expected trace log containing %q, got %+v", want, logger.traces)
		}
	}
}

func TestRunLogsOutsideGitRepository(t *testing.T) {
	setupXDGConfigHome(t)

	logger := &testLogger{}
	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
		Logger: logger,
	}

	_, err := service.Run(InitRequest{AutoApprove: true})
	if !errors.Is(err, internal.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
	if !internal.IsReported(err) {
		t.Fatalf("expected reported error wrapper, got %v", err)
	}
	if !logger.containsError("erun config is not initialized. Run erun in project directory.") {
		t.Fatalf("expected error log, got %+v", logger.errors)
	}
}

func TestRunTenantConfirmationRejected(t *testing.T) {
	setupXDGConfigHome(t)

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
		Confirm: func(label string) (bool, error) {
			return false, nil
		},
	}

	if _, err := service.Run(InitRequest{}); !errors.Is(err, ErrTenantInitializationCancelled) {
		t.Fatalf("expected tenant cancellation, got %v", err)
	}
}

func TestRunEnvironmentConfirmationRejected(t *testing.T) {
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        "/tmp/project",
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return tenant, "/tmp/project", nil
		},
		FindCurrentBranch: func(string) (string, error) {
			return "develop", nil
		},
		Confirm: func(label string) (bool, error) {
			want := EnvironmentConfirmationLabelWithBranch(tenant, DefaultEnvironment, "develop")
			if label != want {
				t.Fatalf("unexpected confirmation label: %q", label)
			}
			return false, nil
		},
	}

	if _, err := service.Run(InitRequest{DetectEnvironmentBranch: true}); !errors.Is(err, ErrEnvironmentInitializationCancelled) {
		t.Fatalf("expected environment cancellation, got %v", err)
	}
}

func TestRunConfirmationError(t *testing.T) {
	setupXDGConfigHome(t)

	expectedErr := errors.New("boom")
	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
		Confirm: func(label string) (bool, error) {
			return false, expectedErr
		},
	}

	if _, err := service.Run(InitRequest{}); !errors.Is(err, expectedErr) {
		t.Fatalf("expected confirm error, got %v", err)
	}
}

func TestRunPropagatesSaveErrors(t *testing.T) {
	setupXDGConfigHome(t)

	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "erun")
	if err := os.MkdirAll(configPath, 0o555); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(configPath, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	service := Service{
		Store: ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
		Confirm: func(label string) (bool, error) {
			return true, nil
		},
	}

	if _, err := service.Run(InitRequest{}); !errors.Is(err, internal.ErrFailedToSaveConfig) {
		t.Fatalf("expected save error, got %v", err)
	}
}

type testLogger struct {
	traces []string
	errors []string
}

func (l *testLogger) Trace(message string) {
	l.traces = append(l.traces, message)
}

func (l *testLogger) Error(message string) {
	l.errors = append(l.errors, message)
}

func (l *testLogger) containsTrace(want string) bool {
	for _, got := range l.traces {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

func (l *testLogger) containsError(want string) bool {
	for _, got := range l.errors {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}
