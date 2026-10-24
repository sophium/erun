// Package env builds an isolated environment for one erun integration scenario.
package env

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// shellUtilities are forwarded onto PATH for the suite's stub scripts only; never add a tool erun itself invokes.
var shellUtilities = []string{"cat", "dirname", "mkdir", "sleep", "touch", "tr", "wc"}

// hostTools are real host executables erun needs (git for real repos, tar for the mirror fetch lane),
// routed through ERUN_<NAME>_BIN rather than PATH. A later duplicate ERUN_<NAME>_BIN wins.
var hostTools = []string{"git", "tar"}

// Setup is the resolved environment for a single subprocess invocation.
type Setup struct {
	Home       string
	ConfigHome string
	CacheHome  string
	DataHome   string
	Cwd        string
	// PathDir is the scenario's entire PATH; the host PATH is not inherited.
	PathDir  string
	toolBins []string
	// stubShell is the absolute POSIX shell the Windows stub runner needs.
	stubShell string
}

// Env returns a fresh slice each call so callers can append; a later duplicate var wins.
func (s Setup) Env() []string {
	return append([]string{
		"HOME=" + s.Home,
		"XDG_CONFIG_HOME=" + s.ConfigHome,
		"XDG_CACHE_HOME=" + s.CacheHome,
		"XDG_DATA_HOME=" + s.DataHome,
		// Windows resolves home from these, not HOME/XDG; inert on Unix.
		"USERPROFILE=" + s.Home,
		"LOCALAPPDATA=" + s.ConfigHome,
		"APPDATA=" + s.ConfigHome,
		// Only the forwarders, so behavior matches across hosts; external tools must be stubbed.
		"PATH=" + s.PathDir,
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
		// Keeps skills baked into a host image out of doctor output.
		"ERUN_SKILLS_DIR=" + filepath.Join(s.Home, ".no-baked-skills"),
		// Deploy never probes a real registry: only erun-devops is published, at any version.
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*",
		// The live MCP-auth probe reads as unknown, so deploy never reads helm.
		"ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=",
		// A stub cannot find a shell on the scrubbed PATH; empty without a POSIX shell (Windows only).
		"ERUN_STUB_SH=" + s.stubShell,
	}, s.toolBins...)
}

func New(t testing.TB) Setup {
	t.Helper()
	root := t.TempDir()
	// macOS $TMPDIR is a symlink, while the binary reports physical paths.
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
	// This is git's global config for the CLI under test: automatic maintenance off.
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(harnessexec.MaintenanceOffGitConfig), 0o644); err != nil {
		t.Fatalf("write scenario git config: %v", err)
	}
	return Setup{
		Home:       home,
		ConfigHome: cfg,
		CacheHome:  cache,
		DataHome:   data,
		Cwd:        cwd,
		PathDir:    scrubbedPathDir(t, filepath.Join(root, "path")),
		toolBins:   hostToolBins(t),
		stubShell:  stubShellPath(t),
	}
}

// stubShellPath resolves sh, falling back on Windows to the one Git for Windows ships beside git.
func stubShellPath(t testing.TB) string {
	t.Helper()
	if path, err := osexec.LookPath("sh"); err == nil {
		return path
	}
	git, err := osexec.LookPath("git")
	if err != nil {
		return ""
	}
	gitRoot := filepath.Dir(filepath.Dir(git))
	for _, candidate := range []string{
		filepath.Join(gitRoot, "usr", "bin", "sh.exe"),
		filepath.Join(gitRoot, "bin", "sh.exe"),
	} {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// scrubbedPathDir holds one forwarder per shellUtilities entry, keeping ambient host tools off PATH.
// Forwarders, not symlinks, because Windows needs elevation to create symlinks.
func scrubbedPathDir(t testing.TB, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range shellUtilities {
		target := hostBinary(t, name)
		body := "#!/bin/sh\n# erun integration PATH forwarder\nexec '" + filepath.ToSlash(target) + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write PATH forwarder %s: %v", name, err)
		}
	}
	return dir
}

func hostToolBins(t testing.TB) []string {
	t.Helper()
	out := make([]string, 0, len(hostTools))
	for _, name := range hostTools {
		seam := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
		out = append(out, seam+"="+hostBinary(t, name))
	}
	return out
}

func hostBinary(t testing.TB, name string) string {
	t.Helper()
	path, err := osexec.LookPath(name)
	if err != nil {
		t.Fatalf("the integration suite needs %q on the host PATH: %v", name, err)
	}
	return path
}
