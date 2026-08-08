// Package env builds an isolated execution environment for a single erun
// integration scenario, so subprocesses see deterministic paths and don't
// collide with each other or with the developer's real config.
package env

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shellUtilities are the POSIX utilities the suite's own stub scripts invoke
// (counters, marker files, heredocs). They are forwarded into every scenario's
// PATH because a stub runs as a child of the binary under test and inherits its
// PATH; everything else the host has installed stays unreachable. Adding a name
// here is a deliberate act: it must be a utility a stub script needs, never a
// tool erun itself invokes.
var shellUtilities = []string{"cat", "dirname", "mkdir", "sleep", "touch", "tr", "wc"}

// hostTools are the host executables erun itself may resolve, forwarded through
// their declared ERUN_<NAME>_BIN seam rather than through PATH. git is the
// suite's one irreducible host dependency: the fixtures build real repositories
// with it and the release/diff/exec scenarios read real git state, so no stub
// could stand in for it. A scenario that wants a scripted git appends its own
// ERUN_GIT_BIN after Env() (the later duplicate wins).
var hostTools = []string{"git"}

// Setup is the resolved environment for a single subprocess invocation.
type Setup struct {
	Home       string
	ConfigHome string
	CacheHome  string
	DataHome   string
	Cwd        string
	// PathDir is the scenario's entire PATH: the host's PATH is deliberately
	// not inherited, so a command can only reach an external binary the
	// scenario declared. See scrubbedPathDir.
	PathDir string
	// toolBins routes hostTools to their absolute host paths.
	toolBins []string
	// stubShell is the absolute POSIX shell the Windows stub runner needs.
	stubShell string
}

// Env returns the subprocess environment as a fresh slice each call, so
// callers can append scenario-specific vars.
func (s Setup) Env() []string {
	return append([]string{
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
		// The scenario's PATH holds nothing but the utility forwarders in
		// scrubbedPathDir. A command under test therefore behaves the same on a
		// laptop, in the agent pod, and in the runtime image's test stage — none
		// of which agree about what is installed — and a scenario that needs
		// kubectl/helm/docker/aws must declare a stub for it.
		"PATH=" + s.PathDir,
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
		// Answer the "does the live release already have MCP auth enabled?" probe
		// as unknown, so a deploy that resolves no MCP auth never reads helm and
		// no scenario depends on a real release (or consumes a helm stub call).
		// Scenarios that exercise the downgrade guard append their own
		// ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE after Env() (the later duplicate wins).
		"ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=",
		// The shell the Windows stub runner launches a stub's script body with.
		// A stub inherits the scrubbed PATH, so it cannot find one there; this is
		// the same absolute-path routing hostTools uses for git. Empty when the
		// host has no POSIX shell, which only matters on Windows.
		"ERUN_STUB_SH=" + s.stubShell,
	}, s.toolBins...)
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
		PathDir:    scrubbedPathDir(t, filepath.Join(root, "path")),
		toolBins:   hostToolBins(t),
		stubShell:  stubShellPath(t),
	}
}

// stubShellPath resolves the POSIX shell a stub script body runs under. On Unix
// it is whatever `sh` the host has. On Windows the shell is not on the scenario
// PATH and Windows cannot execute a shebang script at all, so the stub runner
// needs an absolute path to one — and Git for Windows ships it beside the git
// the suite already requires, which is why deriving it from the resolved git is
// enough rather than adding a second host prerequisite.
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

// scrubbedPathDir builds the one directory a scenario's PATH points at: a
// forwarder per shellUtilities entry, and nothing else. Forwarders rather than
// the host's own bin directories, because those also hold whatever tools the
// developer installed — the ambient kubectl that made a golden depend on the
// recording machine. Forwarders rather than symlinks, because Windows hosts
// cannot create them without elevation.
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

// hostToolBins routes each hostTools entry to its absolute host path through the
// ERUN_<NAME>_BIN seam production already honors, so the tool stays reachable
// with the PATH scrubbed.
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
