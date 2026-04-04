package internal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

const testConfigRoot = "erun"

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

func TestSaveAndLoadERunConfig(t *testing.T) {
	setupXDGConfigHome(t)

	expected := ERunConfig{DefaultTenant: "tenant-a"}
	if err := SaveERunConfig(expected); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}

	cfg, path, err := LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}

	if cfg != expected {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if filepath.Base(path) != configFile {
		t.Fatalf("unexpected config path: %s", path)
	}
}

func TestLoadERunConfigNotInitialized(t *testing.T) {
	setupXDGConfigHome(t)

	_, _, err := LoadERunConfig()
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

func TestLoadERunConfigCorrupted(t *testing.T) {
	setupXDGConfigHome(t)

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
	setupXDGConfigHome(t)

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
	setupXDGConfigHome(t)

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
	setupXDGConfigHome(t)

	cfg := TenantConfig{ProjectRoot: "/tmp/project", Name: "tenant-a", DefaultEnvironment: "dev"}
	if err := SaveTenantConfig(cfg); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}

	loaded, _, err := LoadTenantConfig(cfg.Name)
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}

	if loaded != cfg {
		t.Fatalf("unexpected tenant config: %+v", loaded)
	}
}

func TestListTenantConfigs(t *testing.T) {
	setupXDGConfigHome(t)

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

func TestLoadTenantConfigErrors(t *testing.T) {
	setupXDGConfigHome(t)

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
	setupXDGConfigHome(t)

	tenant := "tenant-a"
	configPath, err := xdg.ConfigFile(filepath.Join(testConfigRoot, tenant, configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	dir := filepath.Dir(configPath)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(dir, []byte(""), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{Name: tenant}); !errors.Is(err, ErrNoUserDataFolder) {
		t.Fatalf("expected ErrNoUserDataFolder, got %v", err)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	if err := os.MkdirAll(dir, 0o555); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	if err := SaveTenantConfig(TenantConfig{Name: tenant}); !errors.Is(err, ErrFailedToSaveConfig) {
		t.Fatalf("expected ErrFailedToSaveConfig, got %v", err)
	}
}

func TestEnvConfigRoundTrip(t *testing.T) {
	setupXDGConfigHome(t)

	cfg := EnvConfig{Name: "dev", RepoPath: "/tmp/project-dev", Branch: "develop"}
	if err := SaveEnvConfig("tenant-a", cfg); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	loaded, _, err := LoadEnvConfig("tenant-a", cfg.Name)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}

	if loaded != cfg {
		t.Fatalf("unexpected env config: %+v", loaded)
	}
}

func TestLoadEnvConfigErrors(t *testing.T) {
	setupXDGConfigHome(t)

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
	setupXDGConfigHome(t)

	path, err := xdg.ConfigFile(filepath.Join(testConfigRoot, "tenant-a", "dev", configFile))
	if err != nil {
		t.Fatalf("xdg path: %v", err)
	}
	dir := filepath.Dir(path)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(dir, []byte(""), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev"}); !errors.Is(err, ErrNoUserDataFolder) {
		t.Fatalf("expected ErrNoUserDataFolder, got %v", err)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	if err := os.MkdirAll(dir, 0o555); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	if err := SaveEnvConfig("tenant-a", EnvConfig{Name: "dev"}); !errors.Is(err, ErrFailedToSaveConfig) {
		t.Fatalf("expected ErrFailedToSaveConfig, got %v", err)
	}
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
