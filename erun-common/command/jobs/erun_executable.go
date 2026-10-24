package jobs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveErunExecutable finds the erun binary, preferring a sibling of the running
// program over PATH so a source build uses the binary built beside it.
func ResolveErunExecutable() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ERUN_ERUN_BIN")); override != "" {
		return override, nil
	}
	name := ErunExecutableName()
	if sibling := erunExecutableNearRunningProgram(name); sibling != "" {
		return sibling, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("the erun executable was not found beside this program or on PATH: %w", err)
	}
	return path, nil
}

// ErunExecutableName is the erun binary's file name on this host.
func ErunExecutableName() string {
	if runtime.GOOS == "windows" {
		return "erun.exe"
	}
	return "erun"
}

// erunExecutableNearRunningProgram searches beside the running program, which
// inside a macOS .app sits three levels below its siblings' directory.
func erunExecutableNearRunningProgram(name string) string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return erunExecutableNear(executable, name)
}

func erunExecutableNear(executable, name string) string {
	for _, root := range executableSiblingRoots(filepath.Dir(executable)) {
		for _, candidate := range []string{
			filepath.Join(root, name),
			filepath.Clean(filepath.Join(root, "..", "..", "erun-cli", "bin", name)),
		} {
			if candidate == executable {
				continue
			}
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func executableSiblingRoots(dir string) []string {
	roots := []string{dir}
	if container := macOSBundleContainer(dir); container != "" {
		roots = append(roots, container)
	}
	return roots
}

// macOSBundleContainer returns "" for anything but an exact <Name>.app/Contents/MacOS dir.
func macOSBundleContainer(dir string) string {
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	bundle := filepath.Dir(contents)
	if filepath.Ext(bundle) != ".app" {
		return ""
	}
	return filepath.Dir(bundle)
}
