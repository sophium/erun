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
	configDir, err := ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir failed: %v", err)
	}

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
	if result.ConfigDirectory != configDir {
		t.Fatalf("unexpected config directory: %+v", result)
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
	if !result.CurrentDirectory.Effective.Snapshot {
		t.Fatalf("expected effective snapshot to default on, got %+v", result.CurrentDirectory.Effective)
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
	if !result.CurrentDirectory.Effective.Snapshot {
		t.Fatalf("expected effective snapshot to default on, got %+v", result.CurrentDirectory.Effective)
	}
}

func TestResolveListResultUsesEffectiveKubernetesContextForCurrentDirectoryTarget(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: DefaultEnvironment},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/local": {Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "rancher-desktop"},
			},
			resolveEffectiveKubernetesContext: func(environment, configured string) string {
				if environment != DefaultEnvironment || configured != "rancher-desktop" {
					t.Fatalf("unexpected resolver inputs: environment=%q configured=%q", environment, configured)
				}
				return "docker-desktop"
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "rancher-desktop"}},
		},
	}

	result, err := ResolveListResult(store, nil, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("ResolveListResult failed: %v", err)
	}
	if result.CurrentDirectory.Effective == nil {
		t.Fatalf("expected effective target, got %+v", result.CurrentDirectory)
	}
	if result.CurrentDirectory.Effective.KubernetesContext != "docker-desktop" {
		t.Fatalf("unexpected effective kubernetes context: %+v", result.CurrentDirectory.Effective)
	}
	if !result.CurrentDirectory.Effective.Snapshot {
		t.Fatalf("expected effective snapshot to default on, got %+v", result.CurrentDirectory.Effective)
	}
	if got := result.Tenants[0].Environments[0].KubernetesContext; got != "rancher-desktop" {
		t.Fatalf("expected configured tenant environment context to remain unchanged, got %q", got)
	}
}

func TestResolveListResultIncludesSnapshotPreferenceForTenant(t *testing.T) {
	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	snapshot := false
	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: DefaultEnvironment, Snapshot: &snapshot},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/local": {Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "cluster-local"},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "cluster-local"}},
		},
	}

	result, err := ResolveListResult(store, func() (string, string, error) {
		return "tenant-a", repoRoot, nil
	}, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("ResolveListResult failed: %v", err)
	}
	if result.CurrentDirectory.Effective == nil || result.CurrentDirectory.Effective.Snapshot {
		t.Fatalf("expected effective snapshot off, got %+v", result.CurrentDirectory.Effective)
	}
	if len(result.Tenants) != 1 || len(result.Tenants[0].Environments) != 1 || result.Tenants[0].Snapshot {
		t.Fatalf("expected environment snapshot off, got %+v", result.Tenants)
	}
}

func TestResolveListResultIncludesSSHDConfiguration(t *testing.T) {
	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "dev", Remote: true},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					RepoPath:          repoRoot,
					KubernetesContext: "cluster-dev",
					Remote:            true,
					SSHD: SSHDConfig{
						Enabled:       true,
						LocalPort:     DefaultSSHLocalPort,
						PublicKeyPath: "/tmp/id_ed25519.pub",
					},
				},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{
				Name:              "dev",
				RepoPath:          repoRoot,
				KubernetesContext: "cluster-dev",
				Remote:            true,
				SSHD: SSHDConfig{
					Enabled:       true,
					LocalPort:     DefaultSSHLocalPort,
					PublicKeyPath: "/tmp/id_ed25519.pub",
				},
			}},
		},
	}

	result, err := ResolveListResult(store, func() (string, string, error) {
		return "", "", ErrNotInGitRepository
	}, OpenParams{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if err != nil {
		t.Fatalf("ResolveListResult failed: %v", err)
	}
	if result.CurrentDirectory.Effective == nil || !result.CurrentDirectory.Effective.SSH.Enabled {
		t.Fatalf("expected effective SSH details, got %+v", result.CurrentDirectory.Effective)
	}
	if result.CurrentDirectory.Effective.SSH.User != DefaultSSHUser || result.CurrentDirectory.Effective.SSH.LocalPort != DefaultSSHLocalPort {
		t.Fatalf("unexpected effective SSH info: %+v", result.CurrentDirectory.Effective.SSH)
	}
	if got := result.Tenants[0].Environments[0].SSH.WorkspacePath; got != "/home/erun/git/tenant-a" {
		t.Fatalf("unexpected SSH workspace path: %q", got)
	}
}
