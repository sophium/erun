package jobs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveErunExecutable finds the erun binary for a transport that is not itself
// that binary — the in-environment MCP server supervising a job, the desktop app
// wiring a per-env MCP proxy — so those callers get a clear failure rather than a
// launch that silently never happens. A sibling of the running program is
// preferred over PATH so a source build resolves the binary it was built beside
// instead of an unrelated installed one.
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

// ErunExecutableName is the on-disk file name of the erun binary for this host.
func ErunExecutableName() string {
	if runtime.GOOS == "windows" {
		return "erun.exe"
	}
	return "erun"
}

// erunExecutableNearRunningProgram searches beside the running program: the
// install layout puts every erun executable in one directory, and a source build
// puts them in each module's own bin/. It is not the mirror of how the CLI finds
// erun-app — that direction stops at the .app bundle, while a program running
// from inside one sits three levels below the directory its siblings share.
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

// executableSiblingRoots lists the directories an erun executable may sit in
// relative to the running program: its own directory, plus the directory holding
// the macOS .app bundle it runs inside, if any.
func executableSiblingRoots(dir string) []string {
	roots := []string{dir}
	if container := macOSBundleContainer(dir); container != "" {
		roots = append(roots, container)
	}
	return roots
}

// macOSBundleContainer returns the directory holding the <Name>.app bundle whose
// Contents/MacOS is dir, and "" for every other layout — so no other host's
// directory names can accidentally match the bundle shape.
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
