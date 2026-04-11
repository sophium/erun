package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

type listStore struct {
	openStore
	envsByTenant map[string][]EnvConfig
}

func (s listStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	if envs, ok := s.envsByTenant[tenant]; ok {
		return envs, nil
	}
	return nil, nil
}

func TestResolveListResultUsesCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})

	tenantAPath := filepath.Join(t.TempDir(), "tenant-a")
	tenantBPath := filepath.Join(t.TempDir(), "tenant-b")
	subDir := filepath.Join(tenantBPath, "nested")
	for _, dir := range []string{tenantAPath, tenantBPath, subDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tenantBPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"},
				"tenant-b": {Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/local": {Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-a"},
				"tenant-b/dev":   {Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-a"}},
			"tenant-b": {{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}},
		},
	}

	result, err := ResolveListResult(store, nil, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("ResolveListResult failed: %v", err)
	}

	if result.Defaults.Tenant != "tenant-a" || result.Defaults.Environment != "local" {
		t.Fatalf("unexpected defaults: %+v", result.Defaults)
	}
	if result.CurrentDirectory.Repo != "tenant-b" || result.CurrentDirectory.ConfiguredTenant != "tenant-b" {
		t.Fatalf("unexpected current directory: %+v", result.CurrentDirectory)
	}
	if result.CurrentDirectory.Effective == nil {
		t.Fatalf("expected effective target, got %+v", result.CurrentDirectory)
	}
	if result.CurrentDirectory.Effective.Tenant != "tenant-b" || result.CurrentDirectory.Effective.Environment != "dev" {
		t.Fatalf("unexpected effective target: %+v", result.CurrentDirectory.Effective)
	}
	if len(result.Tenants) != 2 {
		t.Fatalf("unexpected tenants: %+v", result.Tenants)
	}
}

func TestResolveListResultFallsBackToDefaultWhenRepoIsNotConfiguredTenant(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "frs")
	subDir := filepath.Join(repoRoot, "nested")
	defaultRepo := filepath.Join(t.TempDir(), "tenant-a")
	for _, dir := range []string{defaultRepo, subDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: defaultRepo, DefaultEnvironment: "dev"},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/dev": {Name: "dev", RepoPath: defaultRepo, KubernetesContext: "cluster-a"},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: "dev", RepoPath: defaultRepo, KubernetesContext: "cluster-a"}},
		},
	}

	result, err := ResolveListResult(store, nil, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("ResolveListResult failed: %v", err)
	}

	if result.CurrentDirectory.Repo != "frs" || result.CurrentDirectory.ConfiguredTenant != "" {
		t.Fatalf("unexpected current directory: %+v", result.CurrentDirectory)
	}
	if result.CurrentDirectory.Effective == nil {
		t.Fatalf("expected effective target, got %+v", result.CurrentDirectory)
	}
	if result.CurrentDirectory.Effective.Tenant != "tenant-a" || result.CurrentDirectory.Effective.Environment != "dev" {
		t.Fatalf("unexpected effective target: %+v", result.CurrentDirectory.Effective)
	}
}
