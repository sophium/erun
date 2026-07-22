// Package env builds an isolated execution environment for a single erun
// integration scenario, so subprocesses see deterministic paths and don't
// collide with each other or with the developer's real config.
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

// Env returns the subprocess environment as a fresh slice each call, so
// callers can append scenario-specific vars.
func (s Setup) Env() []string {
	path := os.Getenv("PATH")
	return []string{
		"HOME=" + s.Home,
		"XDG_CONFIG_HOME=" + s.ConfigHome,
		"XDG_CACHE_HOME=" + s.CacheHome,
		"XDG_DATA_HOME=" + s.DataHome,
		// Windows home resolution reads %USERPROFILE%/%LOCALAPPDATA%, not HOME/XDG,
		// so isolate them too — otherwise erun resolves the real user profile (e.g.
		// the trace-log path fails with "%userprofile% is not defined"). Inert on
		// Unix, where erun ignores these, so macOS/Linux goldens are unaffected.
		"USERPROFILE=" + s.Home,
		"LOCALAPPDATA=" + s.ConfigHome,
		"APPDATA=" + s.ConfigHome,
		"PATH=" + path,
		// LANG is required by some path-handling code that calls into glibc.
		"LANG=C.UTF-8",
		// Prevent the subprocess from picking up the developer's terminal
		// config and accidentally writing colored output that destabilizes
		// goldens.
		"TERM=dumb",
		"NO_COLOR=1",
		// Point the agent-skills seam at a non-existent path so a host that
		// bakes skills into its image can't leak them into doctor output and
		// drift the in_runtime_* goldens. Scenarios that exercise the skills
		// row append their own ERUN_SKILLS_DIR after Env() (the later
		// duplicate wins).
		"ERUN_SKILLS_DIR=" + filepath.Join(s.Home, ".no-baked-skills"),
		// Answer the published-runtime-chart existence probe from a static
		// (empty) list so deploy never reaches a real registry and drifts a
		// golden by whatever charts happen to be published there. Scenarios
		// that exercise the tenant-preferred branch append their own
		// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE after Env() (the later duplicate
		// wins).
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=",
	}
}

// New creates a fresh Setup rooted at a per-test temp directory.
func New(t testing.TB) Setup {
	t.Helper()
	root := t.TempDir()
	// Canonicalize to the physical path: macOS $TMPDIR lives under
	// /var/folders, a symlink into /private/var, while the binary under test
	// always reports physical paths. Without this, path-equality checks (e.g.
	// tenant projectroot matching) flake on whichever TMPDIR spelling the
	// invoking shell used.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
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
