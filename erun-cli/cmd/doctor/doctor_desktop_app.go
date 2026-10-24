package doctor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

var (
	desktopAppStat     = os.Stat
	desktopAppReadFile = os.ReadFile
	// Test seam for the system /Applications dir; production never sets it.
	desktopAppSystemApplicationsDirOverrideEnv = func() string {
		return os.Getenv("ERUN_DESKTOP_APP_SYSTEM_APPLICATIONS_DIR_OVERRIDE")
	}
	// Test seam for this executable's own directory; production never sets it.
	desktopAppExecutableDirOverrideEnv = func() string {
		return os.Getenv("ERUN_DESKTOP_APP_EXECUTABLE_DIR_OVERRIDE")
	}
)

// build.sh always writes this key/value shape, so no plist parser is needed.
var cfBundleShortVersionPattern = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)

type installedDesktopApp struct {
	Path    string
	Version string
	// Launched marks the copy `erun app` would start, which need not be first.
	Launched bool
}

// installedDesktopAppCandidates lists the copies `erun app` launches from first,
// then macOS installs with ~/Applications shadowing /Applications.
func installedDesktopAppCandidates(hostOS model.HostOS) []string {
	if hostOS != model.HostOSDarwin {
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
		for _, layout := range shared.DesktopAppLayouts(dir, core.DesktopAppName, hostOS) {
			add(layout.Launch)
		}
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		add(filepath.Join(homeDir, "Applications", shared.DesktopAppBundleName))
	}
	add(filepath.Join(systemApplicationsDir(), shared.DesktopAppBundleName))
	return candidates
}

// desktopAppExecutableDir returns "" when the process cannot name its own executable.
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

// launchedDesktopAppPath uses the launch's own resolution, or "" when there is none.
func launchedDesktopAppPath(hostOS model.HostOS) string {
	dir := desktopAppExecutableDir()
	if dir == "" {
		return ""
	}
	chosen, _ := shared.ResolveAppExecutableNear(filepath.Join(dir, core.DesktopAppName), core.DesktopAppName, hostOS)
	return chosen
}

func systemApplicationsDir() string {
	if override := strings.TrimSpace(desktopAppSystemApplicationsDirOverrideEnv()); override != "" {
		return override
	}
	return "/Applications"
}

func inspectInstalledDesktopApps(hostOS model.HostOS) []installedDesktopApp {
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

// reportInstalledDesktopAppVersion flags bundles that drift from this CLI's version
// and stale duplicates. Host-scoped, so it runs before tenant resolution and under --dry-run.
func reportInstalledDesktopAppVersion(ctx core.Context) error {
	found := inspectInstalledDesktopApps(core.DetectHost().OS)
	if len(found) == 0 {
		return nil
	}
	cliVersion := strings.TrimSpace(shared.CurrentBuildInfo().Version)
	mismatched := installedDesktopAppsMismatching(found, cliVersion)
	if len(mismatched) == 0 && len(found) < 2 {
		return nil
	}

	w := newLineWriter(ctx.Stdout)
	w.Linef("== Desktop app ==")
	for _, app := range found {
		w.Linef("%s: %s", app.Path, app.Version)
	}
	// A matching copy says nothing about which copy a launch reaches.
	if launched := launchedDesktopAppAmong(found); launched != "" {
		w.Linef("erun app launches %s.", launched)
	}
	if len(found) > 1 {
		w.Linef("Multiple %s bundles share the bundle id com.sophium.erun; macOS (Finder, Spotlight, the Dock) can launch either one regardless of which is current. Remove the stale copy(ies) so only the current version can be launched.", shared.DesktopAppBundleName)
	}
	for _, app := range mismatched {
		w.Linef("%s (%s) does not match this CLI (%s). erun has no automated updater for the installed desktop app bundle; rebuild it (./erun-ui/build.sh from a checkout at this version) or reinstall to update it.", app.Path, app.Version, cliVersion)
	}
	w.Linef("")
	return w.Err()
}

func launchedDesktopAppAmong(found []installedDesktopApp) string {
	for _, app := range found {
		if app.Launched {
			return app.Path
		}
	}
	return ""
}

// installedDesktopAppsMismatching flags nothing for a CLI with no stamped version.
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
