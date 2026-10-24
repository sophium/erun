package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

type desktopAppLayout struct {
	// Launch is the .app bundle on macOS and the binary elsewhere.
	Launch string
	// Bundle marks an .app directory; a plain file named ERun.app is not a copy.
	Bundle bool
	// Binary is the executable inside Launch whose mtime moves on an in-place rebuild; empty when Launch is the executable.
	Binary string
}

// DesktopAppLayouts lists, in preference order, where a desktop app copy can sit: beside the CLI, then in erun-ui/bin.
func DesktopAppLayouts(executableDir, executableName string, hostOS model.HostOS) []desktopAppLayout {
	checkoutBin := filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-ui", "bin"))
	if hostOS == model.HostOSDarwin {
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

type presentDesktopApp struct {
	launch   string
	modified time.Time
}

// ResolveAppExecutableNear picks the desktop app copy to launch and the line reporting it.
// The most recently written copy wins so a stale copy cannot shadow a fresh rebuild;
// ties keep layout order.
func ResolveAppExecutableNear(executable, executableName string, hostOS model.HostOS) (string, string) {
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

// layoutModified is the later of the layout's and its inner executable's mtime.
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

// appLaunchTrace names the chosen copy and any copies passed over.
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
