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
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{Name: DefaultEnvironment}); err != nil {
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
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{Name: "prod"}); err != nil {
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
	if result.TenantConfig.Name != "tenant-a" || result.EnvConfig.Name != DefaultEnvironment {
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
		Confirm: func(label string) (bool, error) {
			return false, nil
		},
	}

	if _, err := service.Run(InitRequest{}); !errors.Is(err, ErrEnvironmentInitializationCancelled) {
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
