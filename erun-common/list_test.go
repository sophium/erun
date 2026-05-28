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

