package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// desktopAppLayout is one on-disk layout a desktop app copy can occupy
// relative to this CLI.
type desktopAppLayout struct {
	// Launch is what a launch has to name: on macOS the .app bundle, which is
	// the shape eruncommon.DesktopAppCommand opens as a fresh instance, and
	// elsewhere the binary itself.
	Launch string
	// Bundle marks an .app directory. It is both the reason Binary sits inside
	// Launch and the shape Launch must have to count as a copy at all — a bare
	// file named ERun.app is not one.
	Bundle bool
	// Binary is the path inside Launch whose modification time moves when a
	// rebuild replaces the executable in place instead of recreating the
	// layout. It is empty when Launch is itself the executable.
	Binary string
}

// DesktopAppLayouts lists, in preference order, where a desktop app copy can
// sit relative to the running CLI: beside the binary, which is where a
// packaged install and a hand-placed copy put it, and in the checkout's
// erun-ui/bin, which is where a local ./erun-ui/build.sh writes its rebuild.
func DesktopAppLayouts(executableDir, executableName string, hostOS core.HostOS) []desktopAppLayout {
	checkoutBin := filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-ui", "bin"))
	if hostOS == core.HostOSDarwin {
		bundleBinary := filepath.Join("Contents", "MacOS", executableName)
		return []desktopAppLayout{
			{Launch: filepath.Join(executableDir, DesktopAppBundleName), Bundle: true, Binary: bundleBinary},
			{Launch: filepath.Join(checkoutBin, DesktopAppBundleName), Bundle: true, Binary: bundleBinary},
		}
	}
	return []desktopAppLayout{
		{Launch: filepath.Join(executableDir, executableName)},
		{Launch: filepath.Join(checkoutBin, executableName)},
	}
}

// presentDesktopApp is a candidate layout that exists on disk, with the time
// it was last written.
type presentDesktopApp struct {
	launch   string
	modified time.Time
}

// ResolveAppExecutableNear resolves which copy of the desktop app this CLI
// should launch, and the line reporting the choice.
//
// Two layouts can hold a copy at once, and taking the first that exists lets a
// stale copy beside the CLI shadow a fresh rebuild in erun-ui/bin for good:
// the launch still succeeds, so the desktop quietly keeps running the old
// build, and every restart relaunches that same copy, because a desktop
// restarts from the copy it is running from. The freshness of the copy has to
// decide, not its position in that list.
//
// The one signal both layouts carry on both platforms is when the copy was
// last written, and "newer" means exactly that: the copy whose own
// modification time is greatest wins. For a macOS bundle that time is the
// later of the .app directory and the binary inside it, because a rebuild may
// either recreate the bundle (./erun-ui/build.sh removes and rebuilds it) or
// replace the executable within it. Only a strictly newer copy displaces one
// listed ahead of it, so a tie keeps this list's order and the copy nearest
// the CLI stays the default when the filesystem cannot separate them. A tie is
// the only case freshness cannot answer: a layout that stat reports at all has
// already yielded its own stamp.
func ResolveAppExecutableNear(executable, executableName string, hostOS core.HostOS) (string, string) {
	var present []presentDesktopApp
	for _, layout := range DesktopAppLayouts(filepath.Dir(executable), executableName, hostOS) {
		info, err := os.Stat(layout.Launch)
		if err != nil || info.IsDir() != layout.Bundle {
			continue
		}
		present = append(present, presentDesktopApp{launch: layout.Launch, modified: layoutModified(layout, info)})
	}
	if len(present) == 0 {
		return "", ""
	}
	chosen := present[0]
	for _, candidate := range present[1:] {
		if candidate.modified.After(chosen.modified) {
			chosen = candidate
		}
	}
	return chosen.launch, appLaunchTrace(present, chosen.launch)
}

// layoutModified reports when a candidate copy was last written, taking the
// later of the layout itself and the executable inside it.
func layoutModified(layout desktopAppLayout, info os.FileInfo) time.Time {
	modified := info.ModTime()
	if layout.Binary == "" {
		return modified
	}
	if binary, err := os.Stat(filepath.Join(layout.Launch, layout.Binary)); err == nil && binary.ModTime().After(modified) {
		modified = binary.ModTime()
	}
	return modified
}

// appLaunchTrace names the copy a launch settled on, so an operator whose
// rebuild appears to have done nothing can see which copy actually started.
// With one copy there is nothing to choose between and the line is just the
// answer; with several, the copies passed over are named too.
func appLaunchTrace(present []presentDesktopApp, chosen string) string {
	if len(present) < 2 {
		return fmt.Sprintf("app: launching %s", chosen)
	}
	passedOver := make([]string, 0, len(present)-1)
	for _, candidate := range present {
		if candidate.launch != chosen {
			passedOver = append(passedOver, candidate.launch)
		}
	}
	return fmt.Sprintf("app: launching %s (newest of %d copies found; not launched: %s)",
		chosen, len(present), strings.Join(passedOver, ", "))
}
