package cmd

import (
	"errors"
	"testing"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
)

func TestBuildVersionOutput(t *testing.T) {
	prevV, prevC, prevD := BuildInfo()
	t.Cleanup(func() {
		SetBuildInfo(prevV, prevC, prevD)
	})

	SetBuildInfo("1.2.3", "abcdef", "2024-01-01")
	got := buildVersionOutput()
	if got.Version != "1.2.3" || got.Commit != "abcdef" || got.Date != "2024-01-01" {
		t.Fatalf("unexpected version output: %+v", got)
	}
}

func TestRunInitTool(t *testing.T) {
	store := &stubStore{
		loadERunConfigErr: internal.ErrNotInitialized,
		loadTenantErr:     internal.ErrNotInitialized,
		loadEnvErr:        internal.ErrNotInitialized,
	}
	deps := Dependencies{
		Store: store,
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", "/tmp/project", nil
		},
	}

	got, err := runInitTool(deps, mcpInitInput{})
	if err != nil {
		t.Fatalf("runInitTool failed: %v", err)
	}

	if got.Tenant != "tenant-a" || got.ProjectRoot != "/tmp/project" || got.Environment != bootstrap.DefaultEnvironment {
		t.Fatalf("unexpected init output: %+v", got)
	}
	if !got.CreatedERunConfig || !got.CreatedTenantConfig || !got.CreatedEnvConfig {
		t.Fatalf("expected all configs to be created, got %+v", got)
	}
}

func TestRunInitToolWrapsErrors(t *testing.T) {
	deps := Dependencies{
		Store: &stubStore{
			loadERunConfigErr: internal.ErrNotInitialized,
			loadTenantErr:     internal.ErrNotInitialized,
			loadEnvErr:        internal.ErrNotInitialized,
		},
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	}

	if _, err := runInitTool(deps, mcpInitInput{}); err == nil || !errors.Is(err, internal.ErrNotInGitRepository) {
		t.Fatalf("expected wrapped git repo error, got %v", err)
	}
}

type stubStore struct {
	loadERunConfig    internal.ERunConfig
	loadERunConfigErr error
	loadTenantConfig  internal.TenantConfig
	loadTenantErr     error
	loadEnvConfig     internal.EnvConfig
	loadEnvErr        error

	savedERunConfig   internal.ERunConfig
	savedTenantConfig internal.TenantConfig
	savedEnvConfig    internal.EnvConfig
}

func (s *stubStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	return s.loadERunConfig, "", s.loadERunConfigErr
}

func (s *stubStore) SaveERunConfig(config internal.ERunConfig) error {
	s.savedERunConfig = config
	s.loadERunConfig = config
	s.loadERunConfigErr = nil
	return nil
}

func (s *stubStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	return s.loadTenantConfig, "", s.loadTenantErr
}

func (s *stubStore) SaveTenantConfig(config internal.TenantConfig) error {
	s.savedTenantConfig = config
	s.loadTenantConfig = config
	s.loadTenantErr = nil
	return nil
}

func (s *stubStore) LoadEnvConfig(tenant, envName string) (internal.EnvConfig, string, error) {
	return s.loadEnvConfig, "", s.loadEnvErr
}

func (s *stubStore) SaveEnvConfig(tenant string, config internal.EnvConfig) error {
	s.savedEnvConfig = config
	s.loadEnvConfig = config
	s.loadEnvErr = nil
	return nil
}
