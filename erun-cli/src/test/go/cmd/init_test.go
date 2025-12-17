package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	cmdpkg "github.com/sophium/erun/cmd"
	configpkg "github.com/sophium/erun/internal"
)

func TestFindGitRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git: %v", err)
	}
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dirs: %v", err)
	}

	got, err := configpkg.FindGitRoot(nested)
	if err != nil {
		t.Fatalf("FindGitRoot returned error: %v", err)
	}
	if got != repo {
		t.Fatalf("expected git root %q, got %q", repo, got)
	}
}

func TestInitCommandCreatesTenant(t *testing.T) {
	setTestConfigHome(t)

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git: %v", err)
	}
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	cmd := cmdpkg.NewInitCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetIn(strings.NewReader("\n")) // accept proposed name

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command returned error: %v", err)
	}

	tenantName := filepath.Base(repo)

	if _, err := configpkg.LoadTenantConfig(tenantName); err != nil {
		t.Fatalf("expected tenant config to exist: %v", err)
	}

	envCfg, err := configpkg.LoadEnvConfig(tenantName, "default_env")
	if err != nil {
		t.Fatalf("expected env config to exist: %v", err)
	}
	if envCfg.Name != "default_env" {
		t.Fatalf("unexpected env name: %q", envCfg.Name)
	}

	rootCfg, err := configpkg.LoadERunConfig()
	if err != nil {
		t.Fatalf("expected root config to exist: %v", err)
	}
	if rootCfg.Tenant != tenantName {
		t.Fatalf("expected root tenant %q, got %q", tenantName, rootCfg.Tenant)
	}

	if !strings.Contains(buf.String(), "Initialized tenant") {
		t.Fatalf("init output missing success message, got: %q", buf.String())
	}
}

func TestInitCommandSkipsExistingTenant(t *testing.T) {
	setTestConfigHome(t)

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git: %v", err)
	}
	tenantName := filepath.Base(repo)

	if err := configpkg.SaveTenantConfig(configpkg.TenantConfig{Root: tenantName}); err != nil {
		t.Fatalf("seed tenant config: %v", err)
	}

	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	cmd := cmdpkg.NewInitCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetIn(strings.NewReader("\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command returned error: %v", err)
	}

	if !strings.Contains(buf.String(), "already configured") {
		t.Fatalf("expected already configured message, got: %q", buf.String())
	}
}

func setTestConfigHome(t *testing.T) string {
	t.Helper()

	configHome := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatalf("creating test config dir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
	return configHome
}
