package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adrg/xdg"
)

const testConfigRoot = "erun"

func setupConfigTestXDGConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
	return dir
}

func TestSaveAndLoadERunConfig(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	expected := ERunConfig{DefaultTenant: "tenant-a"}
	if err := SaveERunConfig(expected); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}

	cfg, path, err := LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}

	if !reflect.DeepEqual(cfg, expected) {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if filepath.Base(path) != configFile {
		t.Fatalf("unexpected config path: %s", path)
	}
}

func TestLoadERunConfigNotInitialized(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	_, _, err := LoadERunConfig()
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

func TestLoadERunConfigCorrupted(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}

	if err := os.WriteFile(configPath, []byte(":invalid"), 0o644); err != nil {
		t.Fatalf("write corrupted config: %v", err)
	}

	_, _, err = LoadERunConfig()
	if !errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("expected ErrConfigCorrupted, got %v", err)
	}
}

func TestSaveERunConfigDirectoryConflict(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}

	dir := filepath.Dir(configPath)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(dir, []byte("block"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	err = SaveERunConfig(ERunConfig{})
	if !errors.Is(err, ErrNoUserDataFolder) {
		t.Fatalf("expected ErrNoUserDataFolder, got %v", err)
	}
}

func TestSaveERunConfigWriteFailure(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	err = SaveERunConfig(ERunConfig{})
	if !errors.Is(err, ErrFailedToSaveConfig) {
		t.Fatalf("expected ErrFailedToSaveConfig, got %v", err)
	}
}

func TestTenantConfigRoundTrip(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	snapshot := false
	cfg := TenantConfig{ProjectRoot: "/tmp/project", Name: "tenant-a", DefaultEnvironment: "dev", Snapshot: &snapshot}
	if err := SaveTenantConfig(cfg); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}

	loaded, _, err := LoadTenantConfig(cfg.Name)
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("unexpected tenant config: %+v", loaded)
	}
}

func TestListTenantConfigs(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	for _, cfg := range []TenantConfig{
		{Name: "tenant-b", ProjectRoot: "/tmp/b", DefaultEnvironment: "prod"},
		{Name: "tenant-a", ProjectRoot: "/tmp/a", DefaultEnvironment: "dev"},
	} {
		if err := SaveTenantConfig(cfg); err != nil {
			t.Fatalf("SaveTenantConfig(%q) failed: %v", cfg.Name, err)
		}
	}

	tenants, err := ListTenantConfigs()
	if err != nil {
		t.Fatalf("ListTenantConfigs failed: %v", err)
	}

	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %+v", tenants)
	}
	if tenants[0].Name != "tenant-a" || tenants[1].Name != "tenant-b" {
		t.Fatalf("expected tenants to be sorted by name, got %+v", tenants)
	}
}

func TestListTenantConfigsSkipsIncompleteTenantDirectories(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", ProjectRoot: "/tmp/a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}

	incompleteDir, err := xdg.ConfigFile(filepath.Join(testConfigRoot, "tenant-b", configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(incompleteDir), 0o755); err != nil {
		t.Fatalf("mkdir incomplete tenant dir: %v", err)
	}
	if err := os.Remove(incompleteDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove config file: %v", err)
	}

	tenants, err := ListTenantConfigs()
	if err != nil {
		t.Fatalf("ListTenantConfigs failed: %v", err)
	}
	if len(tenants) != 1 || tenants[0].Name != "tenant-a" {
		t.Fatalf("expected only complete tenants, got %+v", tenants)
	}
}

func TestListEnvConfigsSkipsIncompleteEnvironmentDirectories(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if err := SaveTenantConfig(TenantConfig{Name: "tenant-a", ProjectRoot: "/tmp/a", DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev", RepoPath: "/tmp/a", KubernetesContext: "cluster-dev"}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	incompletePath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, "tenant-a", "prod", configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(incompletePath), 0o755); err != nil {
		t.Fatalf("mkdir incomplete env dir: %v", err)
	}
	if err := os.Remove(incompletePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove config file: %v", err)
	}

	envs, err := ListEnvConfigs("tenant-a")
	if err != nil {
		t.Fatalf("ListEnvConfigs failed: %v", err)
	}
	if len(envs) != 1 || envs[0].Name != "dev" {
		t.Fatalf("expected only complete envs, got %+v", envs)
	}
}

func TestLoadTenantConfigErrors(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if _, _, err := LoadTenantConfig("missing"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}

	tenant := "tenant-a"
	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, tenant, configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("-"), 0o644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}

	if _, _, err := LoadTenantConfig(tenant); !errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("expected ErrConfigCorrupted, got %v", err)
	}
}

func TestSaveTenantConfigErrors(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	tenant := "tenant-a"
	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, tenant, configFile))
	requireNoError(t, err, "xdg path")
	dir := filepath.Dir(configPath)

	blockConfigDirWithFile(t, dir)
	requireErrorIs(t, SaveTenantConfig(TenantConfig{Name: tenant}), ErrNoUserDataFolder)

	makeConfigDirReadOnly(t, dir)
	requireErrorIs(t, SaveTenantConfig(TenantConfig{Name: tenant}), ErrFailedToSaveConfig)
}

func blockConfigDirWithFile(t *testing.T, dir string) {
	t.Helper()
	requireNoError(t, os.RemoveAll(dir), "cleanup")
	requireNoError(t, os.MkdirAll(filepath.Dir(dir), 0o755), "mkdir parent")
	requireNoError(t, os.WriteFile(dir, []byte(""), 0o644), "write blocker")
}

func makeConfigDirReadOnly(t *testing.T, dir string) {
	t.Helper()
	requireNoError(t, os.Remove(dir), "remove blocker")
	requireNoError(t, os.MkdirAll(dir, 0o555), "mkdir dir")
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected %v, got %v", target, err)
	}
}

func TestEnvConfigRoundTrip(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	cfg := EnvConfig{
		Name:              "dev",
		RepoPath:          "/tmp/project-dev",
		KubernetesContext: "cluster-dev",
		ContainerRegistry: "erunpaas",
		RuntimeVersion:    "1.2.3",
		SSHD: SSHDConfig{
			Enabled:       true,
			LocalPort:     62222,
			PublicKeyPath: "/tmp/id_ed25519.pub",
		},
	}
	if err := SaveEnvConfig("tenant-a", cfg); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	loaded, _, err := LoadEnvConfig("tenant-a", cfg.Name)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("unexpected env config: %+v", loaded)
	}
}

func TestListEnvConfigs(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	for _, cfg := range []EnvConfig{
		{Name: "prod", RepoPath: "/tmp/prod", KubernetesContext: "cluster-prod"},
		{Name: "dev", RepoPath: "/tmp/dev", KubernetesContext: "cluster-dev"},
	} {
		if err := SaveEnvConfig("tenant-a", cfg); err != nil {
			t.Fatalf("SaveEnvConfig(%q) failed: %v", cfg.Name, err)
		}
	}

	envs, err := ListEnvConfigs("tenant-a")
	if err != nil {
		t.Fatalf("ListEnvConfigs failed: %v", err)
	}

	if len(envs) != 2 {
		t.Fatalf("expected 2 envs, got %+v", envs)
	}
	if envs[0].Name != "dev" || envs[1].Name != "prod" {
		t.Fatalf("expected envs sorted by name, got %+v", envs)
	}
}

func TestProjectConfigRoundTrip(t *testing.T) {
	projectRoot := t.TempDir()

	cfg := ProjectConfig{
		Environments: map[string]ProjectEnvironmentConfig{
			"local": {
				ContainerRegistry: "erunpaas",
				Docker: ProjectDockerConfig{
					SkipIfExists: []string{"erunpaas/base", "erunpaas/erun-ubuntu"},
				},
			},
			"prod": {ContainerRegistry: "registry.example/team"},
		},
	}
	if err := SaveProjectConfig(projectRoot, cfg); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	loaded, path, err := LoadProjectConfig(projectRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("unexpected project config: %+v", loaded)
	}
	if path != filepath.Join(projectRoot, projectConfigDir, configFile) {
		t.Fatalf("unexpected project config path: %s", path)
	}
}

func TestProjectConfigContainerRegistryForEnvironmentFallsBackToLegacyValue(t *testing.T) {
	cfg := ProjectConfig{ContainerRegistry: "legacy-registry"}

	if got := cfg.ContainerRegistryForEnvironment("local"); got != "legacy-registry" {
		t.Fatalf("unexpected container registry: %q", got)
	}
}

func TestProjectConfigSetContainerRegistryForEnvironmentPreservesProjectWideRegistry(t *testing.T) {
	cfg := ProjectConfig{ContainerRegistry: "shared-registry"}

	cfg.SetContainerRegistryForEnvironment("prod", "prod-registry")

	if cfg.ContainerRegistry != "shared-registry" {
		t.Fatalf("expected project-wide registry to be preserved, got %+v", cfg)
	}
	if got := cfg.ContainerRegistryForEnvironment("local"); got != "shared-registry" {
		t.Fatalf("unexpected local registry: %q", got)
	}
	if got := cfg.ContainerRegistryForEnvironment("prod"); got != "prod-registry" {
		t.Fatalf("unexpected prod registry: %q", got)
	}
}

func TestProjectConfigSetContainerRegistryForEnvironmentAvoidsRedundantOverride(t *testing.T) {
	cfg := ProjectConfig{ContainerRegistry: "shared-registry"}

	cfg.SetContainerRegistryForEnvironment("local", "shared-registry")

	if cfg.ContainerRegistry != "shared-registry" {
		t.Fatalf("expected project-wide registry to be preserved, got %+v", cfg)
	}
	if cfg.Environments != nil {
		t.Fatalf("did not expect redundant environment overrides, got %+v", cfg.Environments)
	}
}

func TestProjectConfigNormalizedReleaseConfigDefaults(t *testing.T) {
	cfg := ProjectConfig{}

	got := cfg.NormalizedReleaseConfig()

	if got.MainBranch != DefaultReleaseMainBranch || got.DevelopBranch != DefaultReleaseDevelopBranch {
		t.Fatalf("unexpected release config defaults: %+v", got)
	}
}

func TestProjectConfigNormalizedReleaseConfigUsesConfiguredBranches(t *testing.T) {
	cfg := ProjectConfig{
		Release: ReleaseConfig{
			MainBranch:    "trunk",
			DevelopBranch: "integration",
		},
	}

	got := cfg.NormalizedReleaseConfig()

	if got.MainBranch != "trunk" || got.DevelopBranch != "integration" {
		t.Fatalf("unexpected release config: %+v", got)
	}
}

func TestLoadProjectConfigNotInitialized(t *testing.T) {
	projectRoot := t.TempDir()

	if _, _, err := LoadProjectConfig(projectRoot); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

func TestLoadEnvConfigErrors(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	if _, _, err := LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}

	path, err := xdg.ConfigFile(filepath.Join(testConfigRoot, "tenant-a", "dev", configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	if err := os.WriteFile(path, []byte("bad"), 0o644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}

	if _, _, err := LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("expected ErrConfigCorrupted, got %v", err)
	}
}

func TestSaveEnvConfigErrors(t *testing.T) {
	setupConfigTestXDGConfigHome(t)

	path, err := xdg.ConfigFile(filepath.Join(testConfigRoot, "tenant-a", "dev", configFile))
	requireNoError(t, err, "xdg path")
	dir := filepath.Dir(path)

	blockConfigDirWithFile(t, dir)
	requireErrorIs(t, SaveEnvConfig("tenant-a", EnvConfig{Name: "dev"}), ErrNoUserDataFolder)

	makeConfigDirReadOnly(t, dir)
	requireErrorIs(t, SaveEnvConfig("tenant-a", EnvConfig{Name: "dev"}), ErrFailedToSaveConfig)
}

func TestFindProjectRoot(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("return to original dir: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "project")
	subDir := filepath.Join(repoRoot, "nested")

	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	name, path, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot failed: %v", err)
	}

	if name != "project" {
		t.Fatalf("unexpected repo name: %s", name)
	}
	if path != realRepoRoot {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestFindProjectRootInWorktree(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("return to original dir: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "project-dev")
	subDir := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: /tmp/worktree"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	name, path, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot failed: %v", err)
	}
	if name != "project-dev" {
		t.Fatalf("unexpected repo name: %s", name)
	}
	if path != realRepoRoot {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestFindProjectRootNotInRepository(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("return to original dir: %v", err)
		}
	})

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, _, err := FindProjectRoot(); !errors.Is(err, ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}
