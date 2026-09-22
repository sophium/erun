package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

var (
	desktopAppStat     = os.Stat
	desktopAppReadFile = os.ReadFile
	// desktopAppSystemApplicationsDirOverrideEnv is a test seam so the
	// integration suite can point the system-wide candidate at a fixture
	// directory instead of the real /Applications; production never sets it.
	desktopAppSystemApplicationsDirOverrideEnv = func() string {
		return os.Getenv("ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE")
	}
	// desktopAppExecutableDirOverrideEnv is the same kind of seam for the
	// candidate that comes from this executable's own directory -- where a
	// packaged install and the dev wrapper's BIN_DIR both put the bundle the
	// CLI actually launches. A scenario cannot write beside the shared
	// instrumented binary without leaking a bundle into every other scenario
	// in the run, so it points this at a directory it owns instead.
	// Production never sets it.
	desktopAppExecutableDirOverrideEnv = func() string {
		return os.Getenv("ERUN_DESKTOP_APP_EXECUTABLE_DIR_OVERRIDE")
	}
)

// desktopAppBundleName is the macOS app bundle erun-ui/build.sh produces.
const desktopAppBundleName = "ERun.app"

// cfBundleShortVersionPattern extracts CFBundleShortVersionString out of a
// standard Info.plist without a full plist parser -- build.sh always writes
// this exact key/value shape, and a minimal pattern keeps this reader
// tolerant of a hand-edited or differently ordered plist too.
var cfBundleShortVersionPattern = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)

type installedDesktopApp struct {
	Path    string
	Version string
	// Launched is true for the copy `erun app` would actually start, which is
	// not necessarily the first one on this list: that resolution picks the
	// newest copy among the layouts beside this CLI, and a copy in
	// /Applications is not one of them.
	Launched bool
}

// installedDesktopAppCandidates lists where a copy of the desktop app bundle
// could live.
//
// The copies `erun app` itself launches from come first -- beside this
// executable, and the checkout's erun-ui/bin -- because those are the ones a
// running desktop actually started from, and a check that only looked at
// /Applications reported on a bundle nothing was running while the live app
// was a different build at a path it never inspected. The dev wrapper's
// BIN_DIR (erun-cli/bin, or $ERUN_DEV_BIN_DIR) is exactly this executable's
// directory, which is why the sibling candidate covers it without knowing
// about the wrapper at all.
//
// The macOS install locations follow, in the order macOS itself favors when
// two copies share the same bundle id: a user's ~/Applications install
// shadows the system-wide /Applications one -- the same convention already
// applied to IntelliJ discovery (resolveInstalledIntelliJContentsDir in
// open_ide.go). Only darwin ships this bundle shape: Homebrew/Scoop always
// build the CLI and the desktop binary from the same tagged source into the
// same keg, so neither package manager ever leaves a second,
// independently-aging copy behind the way a manually built and relocated .app
// bundle can.
func installedDesktopAppCandidates(hostOS common.HostOS) []string {
	if hostOS != common.HostOSDarwin {
		return nil
	}
	seen := make(map[string]struct{}, 4)
	candidates := make([]string, 0, 4)
	add := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}

	if dir := desktopAppExecutableDir(); dir != "" {
		for _, layout := range desktopAppLayouts(dir, common.DesktopAppName, hostOS) {
			add(layout.Launch)
		}
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		add(filepath.Join(homeDir, "Applications", desktopAppBundleName))
	}
	add(filepath.Join(systemApplicationsDir(), desktopAppBundleName))
	return candidates
}

// desktopAppExecutableDir is the directory this CLI is running from, which is
// where `erun app` looks first for the desktop app to launch. A process that
// cannot name its own executable yields no sibling candidate rather than a
// guessed one.
func desktopAppExecutableDir() string {
	if override := strings.TrimSpace(desktopAppExecutableDirOverrideEnv()); override != "" {
		return override
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(executable)
}

// launchedDesktopAppPath is the copy `erun app` would start right now, or ""
// when there is none -- the same resolution the launch itself uses, so the
// bundle doctor reports on is the bundle a launch would actually reach.
func launchedDesktopAppPath(hostOS common.HostOS) string {
	dir := desktopAppExecutableDir()
	if dir == "" {
		return ""
	}
	chosen, _ := resolveAppExecutableNear(filepath.Join(dir, common.DesktopAppName), common.DesktopAppName, hostOS)
	return chosen
}

func systemApplicationsDir() string {
	if override := strings.TrimSpace(desktopAppSystemApplicationsDirOverrideEnv()); override != "" {
		return override
	}
	return "/Applications"
}

// inspectInstalledDesktopApps reads every installed desktop app bundle this
// host's OS convention could hold, returning one entry per bundle that
// actually exists on disk, in candidate order.
func inspectInstalledDesktopApps(hostOS common.HostOS) []installedDesktopApp {
	launched := launchedDesktopAppPath(hostOS)
	var found []installedDesktopApp
	for _, bundlePath := range installedDesktopAppCandidates(hostOS) {
		info, err := desktopAppStat(bundlePath)
		if err != nil || !info.IsDir() {
			continue
		}
		version, ok := readDesktopAppBundleVersion(bundlePath)
		if !ok {
			continue
		}
		found = append(found, installedDesktopApp{Path: bundlePath, Version: version, Launched: bundlePath == launched})
	}
	return found
}

func readDesktopAppBundleVersion(bundlePath string) (string, bool) {
	data, err := desktopAppReadFile(filepath.Join(bundlePath, "Contents", "Info.plist"))
	if err != nil {
		return "", false
	}
	match := cfBundleShortVersionPattern.FindSubmatch(data)
	if match == nil {
		return "", false
	}
	version := strings.TrimSpace(string(match[1]))
	if version == "" {
		return "", false
	}
	return version, true
}

// reportInstalledDesktopAppVersion flags an installed desktop app bundle that
// has drifted from this CLI's own version, and a stale second copy sharing
// the bundle id (com.sophium.erun) with a current one -- both invisible
// today, since nothing about launching a stale bundle (Finder, Spotlight, the
// Dock) ever consults the CLI it sits beside. Detection only, and
// unconditional: a stale or duplicated bundle is a fact about this host, not
// about the tenant/environment doctor is scoped to, so this runs before any
// tenant resolves and the same in --dry-run as for real -- there is no live
// action to skip, only files to read.
func reportInstalledDesktopAppVersion(ctx common.Context) error {
	found := inspectInstalledDesktopApps(common.DetectHost().OS)
	if len(found) == 0 {
		return nil
	}
	cliVersion := strings.TrimSpace(currentBuildInfo().Version)
	mismatched := installedDesktopAppsMismatching(found, cliVersion)
	if len(mismatched) == 0 && len(found) < 2 {
		return nil
	}

	w := newLineWriter(ctx.Stdout)
	w.Linef("== Desktop app ==")
	for _, app := range found {
		w.Linef("%s: %s", app.Path, app.Version)
	}
	// Named explicitly because the list alone does not answer the question this
	// section exists for: a copy matching this CLI says nothing about whether
	// the copy a launch reaches is that one. Without this, doctor could report
	// no drift while the live app was a different build at a third path.
	if launched := launchedDesktopAppAmong(found); launched != "" {
		w.Linef("erun app launches %s.", launched)
	}
	if len(found) > 1 {
		w.Linef("Multiple %s bundles share the bundle id com.sophium.erun; macOS (Finder, Spotlight, the Dock) can launch either one regardless of which is current. Remove the stale copy(ies) so only the current version can be launched.", desktopAppBundleName)
	}
	for _, app := range mismatched {
		w.Linef("%s (%s) does not match this CLI (%s). erun has no automated updater for the installed desktop app bundle; rebuild it (./erun-ui/build.sh from a checkout at this version) or reinstall to update it.", app.Path, app.Version, cliVersion)
	}
	w.Linef("")
	return w.Err()
}

// launchedDesktopAppAmong names the inspected copy `erun app` would start, or
// "" when a launch reaches none of them -- which is the ordinary case for an
// installed CLI whose bundle a launch resolves from somewhere the check did
// not enumerate.
func launchedDesktopAppAmong(found []installedDesktopApp) string {
	for _, app := range found {
		if app.Launched {
			return app.Path
		}
	}
	return ""
}

// installedDesktopAppsMismatching returns the copies whose version differs
// from this CLI's own. A CLI that cannot name its own version (a "dev" build
// with none stamped) flags nothing: it has nothing to compare against, and
// inventing a mismatch there would report every bundle on the host.
func installedDesktopAppsMismatching(found []installedDesktopApp, cliVersion string) []installedDesktopApp {
	if cliVersion == "" {
		return nil
	}
	mismatched := make([]installedDesktopApp, 0, len(found))
	for _, app := range found {
		if app.Version != cliVersion {
			mismatched = append(mismatched, app)
		}
	}
	return mismatched
}
