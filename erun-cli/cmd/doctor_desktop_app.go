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
}

// installedDesktopAppCandidates lists where an installed copy of the desktop
// app bundle could live, in the order macOS itself favors when two copies
// share the same bundle id: a user's ~/Applications install shadows the
// system-wide /Applications one -- the same convention already applied to
// IntelliJ discovery (resolveInstalledIntelliJContentsDir in open_ide.go).
// Only darwin ships this bundle shape: Homebrew/Scoop always build the CLI
// and the desktop binary from the same tagged source into the same keg, so
// neither package manager ever leaves a second, independently-aging copy
// behind the way a manually built and relocated .app bundle can.
func installedDesktopAppCandidates(hostOS common.HostOS) []string {
	if hostOS != common.HostOSDarwin {
		return nil
	}
	candidates := make([]string, 0, 2)
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		candidates = append(candidates, filepath.Join(homeDir, "Applications", desktopAppBundleName))
	}
	candidates = append(candidates, filepath.Join(systemApplicationsDir(), desktopAppBundleName))
	return candidates
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
		found = append(found, installedDesktopApp{Path: bundlePath, Version: version})
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
// Dock) ever consults the CLI it sits beside (erun#2139). Detection only, and
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
	mismatched := make([]installedDesktopApp, 0, len(found))
	for _, app := range found {
		if cliVersion != "" && app.Version != cliVersion {
			mismatched = append(mismatched, app)
		}
	}
	if len(mismatched) == 0 && len(found) < 2 {
		return nil
	}

	w := newLineWriter(ctx.Stdout)
	w.Linef("== Desktop app ==")
	for _, app := range found {
		w.Linef("%s: %s", app.Path, app.Version)
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
