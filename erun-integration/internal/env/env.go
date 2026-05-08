// Package env builds an isolated execution environment (HOME, XDG dirs, PATH,
// cwd) for a single erun integration scenario. Tests construct one Setup per
// run so subprocesses see deterministic paths and don't collide with each
// other or with the developer's real config.
package env

import (
	"os"
	"path/filepath"
	"testing"
)

// Setup is the resolved environment for a single subprocess invocation.
type Setup struct {
	Home       string
	ConfigHome string
	CacheHome  string
	DataHome   string
	Cwd        string
}

// Env returns the environment variable list to pass to the subprocess.
// The returned slice is fresh each call so tests can append further vars.
func (s Setup) Env() []string {
	path := os.Getenv("PATH")
	return []string{
		"HOME=" + s.Home,
		"XDG_CONFIG_HOME=" + s.ConfigHome,
		"XDG_CACHE_HOME=" + s.CacheHome,
		"XDG_DATA_HOME=" + s.DataHome,
		"PATH=" + path,
		// LANG is required by some path-handling code that calls into glibc.
		"LANG=C.UTF-8",
		// Prevent the subprocess from picking up the developer's terminal
		// config and accidentally writing colored output that destabilizes
		// goldens.
		"TERM=dumb",
		"NO_COLOR=1",
	}
}

// New creates a fresh Setup rooted at a temp directory unique to the test.
// The temp directory is auto-cleaned by t.TempDir on test completion.
func New(t testing.TB) Setup {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cfg := filepath.Join(home, ".config")
	cache := filepath.Join(home, ".cache")
	data := filepath.Join(home, ".local", "share")
	cwd := filepath.Join(root, "cwd")
	for _, dir := range []string{home, cfg, cache, data, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return Setup{
		Home:       home,
		ConfigHome: cfg,
		CacheHome:  cache,
		DataHome:   data,
		Cwd:        cwd,
	}
}
