package cmd

import (
	"testing"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/opener"
)

func TestOpenCommandLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := opener.ShellLaunchRequest{}
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{repoPath: repoPath},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

type openCommandStore struct {
	repoPath string
}

func (openCommandStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	return internal.ERunConfig{}, "", nil
}

func (openCommandStore) SaveERunConfig(internal.ERunConfig) error {
	return nil
}

func (openCommandStore) ListTenantConfigs() ([]internal.TenantConfig, error) {
	return nil, nil
}

func (s openCommandStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	return internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        s.repoPath,
		DefaultEnvironment: "local",
	}, "", nil
}

func (openCommandStore) SaveTenantConfig(internal.TenantConfig) error {
	return nil
}

func (s openCommandStore) LoadEnvConfig(tenant, env string) (internal.EnvConfig, string, error) {
	return internal.EnvConfig{
		Name:     env,
		RepoPath: s.repoPath,
	}, "", nil
}

func (openCommandStore) SaveEnvConfig(string, internal.EnvConfig) error {
	return nil
}
