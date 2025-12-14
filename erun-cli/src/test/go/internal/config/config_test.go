package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/internal/config"
)

func TestLoadCreatesTenantConfigWithHome(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	loader := config.Loader{Tenant: "mytenant"}
	cfg, path, err := loader.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	wantPath := filepath.Join(tmpHome, ".erun", "mytenant.yaml")
	if path != wantPath {
		t.Fatalf("expected config path %q, got %q", wantPath, path)
	}
	if cfg.Home != filepath.Dir(wantPath) {
		t.Fatalf("expected home %q, got %q", filepath.Dir(wantPath), cfg.Home)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read created config: %v", err)
	}
	if !strings.Contains(string(data), "home:") {
		t.Fatalf("expected config to contain home field, got: %s", string(data))
	}
	if !strings.Contains(string(data), cfg.Home) {
		t.Fatalf("expected config home value to be written, got: %s", string(data))
	}
}

func TestLoadRespectsExplicitPath(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "custom.yaml")
	loader := config.Loader{Path: path}

	cfg, gotPath, err := loader.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if gotPath != path {
		t.Fatalf("expected config path %q, got %q", path, gotPath)
	}
	if cfg.Home != tempDir {
		t.Fatalf("expected home %q, got %q", tempDir, cfg.Home)
	}
}

func TestLoadUsesWindowsHomeFallback(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	fakeDrive := t.TempDir()
	fakePath := "windows-home"
	t.Setenv("HOMEDRIVE", fakeDrive)
	t.Setenv("HOMEPATH", fakePath)

	loader := config.Loader{Tenant: "wintenant"}
	cfg, path, err := loader.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	expectedBase := filepath.Join(fakeDrive, fakePath)
	expectedPath := filepath.Join(expectedBase, ".erun", "wintenant.yaml")
	if path != expectedPath {
		t.Fatalf("expected config path %q, got %q", expectedPath, path)
	}
	if cfg.Home != filepath.Dir(expectedPath) {
		t.Fatalf("expected home %q, got %q", filepath.Dir(expectedPath), cfg.Home)
	}
}

func TestLoadBackfillsMissingHome(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "existing.yaml")
	if err := os.WriteFile(path, []byte("foo: bar\n"), 0o644); err != nil {
		t.Fatalf("failed to prepare config file: %v", err)
	}

	loader := config.Loader{Path: path}
	cfg, _, err := loader.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Home != tempDir {
		t.Fatalf("expected home %q, got %q", tempDir, cfg.Home)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if !strings.Contains(string(data), cfg.Home) {
		t.Fatalf("expected updated home value in file, got: %s", string(data))
	}
}
