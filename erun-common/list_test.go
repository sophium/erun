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
	requireNoError(t, err, "ERunConfigDir failed")

	restoreWorkingDirAfterTest(t)

	tenantAPath := filepath.Join(t.TempDir(), "tenant-a")
	tenantBPath := filepath.Join(t.TempDir(), "tenant-b")
	subDir := filepath.Join(tenantBPath, "nested")
	mkdirAllForTest(t, tenantAPath, tenantBPath, subDir, filepath.Join(tenantBPath, ".git"))
	requireNoError(t, os.Chdir(subDir), "chdir")

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

	requireCurrentDirectoryListResult(t, result, configDir)
}

func restoreWorkingDirAfterTest(t *testing.T) {
	t.Helper()
	originalDir, err := os.Getwd()
	requireNoError(t, err, "getwd")
	t.Cleanup(func() {
		requireNoError(t, os.Chdir(originalDir), "restore working directory")
	})
}

func mkdirAllForTest(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		requireNoError(t, os.MkdirAll(dir, 0o755), "mkdir "+dir)
	}
}

func requireCurrentDirectoryListResult(t *testing.T, result ListResult, configDir string) {
	t.Helper()
	requireCondition(t, result.Defaults.Tenant == "tenant-a" && result.Defaults.Environment == "local", "unexpected defaults: %+v", result.Defaults)
	requireEqual(t, result.ConfigDirectory, configDir, "config directory")
	requireCondition(t, result.CurrentDirectory.Repo == "tenant-b" && result.CurrentDirectory.ConfiguredTenant == "tenant-b", "unexpected current directory: %+v", result.CurrentDirectory)
	requireCondition(t, result.CurrentDirectory.Effective != nil, "expected effective target, got %+v", result.CurrentDirectory)
	requireCondition(t, result.CurrentDirectory.Effective.Tenant == "tenant-b" && result.CurrentDirectory.Effective.Environment == "dev", "unexpected effective target: %+v", result.CurrentDirectory.Effective)
	requireCondition(t, !result.CurrentDirectory.Effective.Snapshot, "expected effective snapshot to default off, got %+v", result.CurrentDirectory.Effective)
	requireEqual(t, len(result.Tenants), 2, "tenant count")
}

func TestResolveListResultFallsBackToDefaultWhenRepoIsNotConfiguredTenant(t *testing.T) {
	restoreWorkingDirAfterTest(t)

	repoRoot := filepath.Join(t.TempDir(), "frs")
	subDir := filepath.Join(repoRoot, "nested")
	defaultRepo := filepath.Join(t.TempDir(), "tenant-a")
	mkdirAllForTest(t, defaultRepo, subDir, filepath.Join(repoRoot, ".git"))
	requireNoError(t, os.Chdir(subDir), "chdir")

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
	requireNoError(t, err, "ResolveListResult failed")

	requireFallbackListResult(t, result)
}

func requireFallbackListResult(t *testing.T, result ListResult) {
	t.Helper()
	requireCondition(t, result.CurrentDirectory.Repo == "frs" && result.CurrentDirectory.ConfiguredTenant == "", "unexpected current directory: %+v", result.CurrentDirectory)
	requireCondition(t, result.CurrentDirectory.Effective != nil, "expected effective target, got %+v", result.CurrentDirectory)
	requireCondition(t, result.CurrentDirectory.Effective.Tenant == "tenant-a" && result.CurrentDirectory.Effective.Environment == "dev", "unexpected effective target: %+v", result.CurrentDirectory.Effective)
	requireCondition(t, !result.CurrentDirectory.Effective.Snapshot, "expected effective snapshot to default off, got %+v", result.CurrentDirectory.Effective)
}

func TestResolveListResultUsesEffectiveKubernetesContextForCurrentDirectoryTarget(t *testing.T) {
	restoreWorkingDirAfterTest(t)

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	mkdirAllForTest(t, filepath.Join(repoRoot, ".git"))
	requireNoError(t, os.Chdir(repoRoot), "chdir")

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
	requireNoError(t, err, "ResolveListResult failed")
	requireEffectiveKubernetesListResult(t, result)
}

func requireEffectiveKubernetesListResult(t *testing.T, result ListResult) {
	t.Helper()
	requireCondition(t, result.CurrentDirectory.Effective != nil, "expected effective target, got %+v", result.CurrentDirectory)
	requireEqual(t, result.CurrentDirectory.Effective.KubernetesContext, "docker-desktop", "effective kubernetes context")
	requireCondition(t, !result.CurrentDirectory.Effective.Snapshot, "expected effective snapshot to default off, got %+v", result.CurrentDirectory.Effective)
	requireEqual(t, result.Tenants[0].Environments[0].KubernetesContext, "rancher-desktop", "configured tenant environment context")
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
				"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: DefaultEnvironment},
			},
			envConfigs: map[string]EnvConfig{
				"tenant-a/local": {Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "cluster-local", RuntimeVersion: "1.0.19-snapshot-20260418141901", Snapshot: &snapshot},
			},
		},
		envsByTenant: map[string][]EnvConfig{
			"tenant-a": {{Name: DefaultEnvironment, RepoPath: repoRoot, KubernetesContext: "cluster-local", RuntimeVersion: "1.0.19-snapshot-20260418141901", Snapshot: &snapshot}},
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
	if len(result.Tenants) != 1 || len(result.Tenants[0].Environments) != 1 || result.Tenants[0].Environments[0].Snapshot {
		t.Fatalf("expected environment snapshot off, got %+v", result.Tenants)
	}
	if result.Tenants[0].Environments[0].RuntimeVersion != "1.0.19-snapshot-20260418141901" {
		t.Fatalf("unexpected runtime version: %+v", result.Tenants[0].Environments[0])
	}
}

func TestResolveListResultIncludesSSHDConfiguration(t *testing.T) {
	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	store := listStore{
		openStore: openStore{
			toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]TenantConfig{
				"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "dev"},
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
	requireNoError(t, err, "ResolveListResult failed")
	requireSSHDListResult(t, result)
}

func requireSSHDListResult(t *testing.T, result ListResult) {
	t.Helper()
	requireCondition(t, result.CurrentDirectory.Effective != nil && result.CurrentDirectory.Effective.SSH.Enabled, "expected effective SSH details, got %+v", result.CurrentDirectory.Effective)
	requireCondition(t, result.CurrentDirectory.Effective.LocalPorts.RangeStart == 17000 && result.CurrentDirectory.Effective.LocalPorts.SSH == DefaultSSHLocalPort, "unexpected effective local ports: %+v", result.CurrentDirectory.Effective.LocalPorts)
	requireEqual(t, result.CurrentDirectory.Effective.SSH.HostAlias, "erun-tenant-a-dev", "effective SSH host alias")
	requireCondition(t, result.CurrentDirectory.Effective.SSH.User == DefaultSSHUser && result.CurrentDirectory.Effective.SSH.LocalPort == DefaultSSHLocalPort, "unexpected effective SSH info: %+v", result.CurrentDirectory.Effective.SSH)
	requireCondition(t, result.Tenants[0].Environments[0].Remote, "expected remote environment flag")
	requireEqual(t, result.Tenants[0].Environments[0].LocalPorts.RangeEnd, 17099, "environment local port range")
	requireEqual(t, result.Tenants[0].Environments[0].SSH.WorkspacePath, "/home/erun/git/tenant-a", "SSH workspace path")
}
