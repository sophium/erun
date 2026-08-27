package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// hostOpenCommand and hostRevealCommand return the OS opener invocation for a
// host path, passed to exec as a discrete argument -- never interpolated into
// a shell command line, since the path can originate from untrusted terminal
// output (see erun-ui/AGENTS.md's HideConsoleWindow discipline for the same
// reasoning applied to every other non-PTY child this module spawns).
func hostOpenCommand(goos, target string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{target}, nil
	case "linux":
		return "xdg-open", []string{target}, nil
	case "windows":
		return "explorer", []string{target}, nil
	default:
		return "", nil, fmt.Errorf("opening host paths is unsupported on %s", goos)
	}
}

// hostRevealCommand reveals a path in the OS file manager. Linux has no
// portable file-select primitive, so it falls back to opening the containing
// directory.
func hostRevealCommand(goos, target string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{"-R", target}, nil
	case "linux":
		return "xdg-open", []string{filepath.Dir(target)}, nil
	case "windows":
		return "explorer", []string{"/select,", target}, nil
	default:
		return "", nil, fmt.Errorf("revealing host paths is unsupported on %s", goos)
	}
}

// launchHostOpenerDetached starts the OS opener and detaches it, mirroring
// launchHostArtifactDetached's discipline: HideConsoleWindow suppresses the
// console flash a windowless desktop child gets on Windows, and Release()
// lets the opener outlive this process.
func launchHostOpenerDetached(executable string, args []string) error {
	cmd := exec.Command(executable, args...)
	eruncommon.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", executable, err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

// OpenHostPath opens a host-machine file or directory with the OS handler.
// Callers must already know this is a host path -- a path from a pod-side
// terminal tab is resolved via ResolveEnvironmentHostPath first, never passed
// here directly.
func (a *App) OpenHostPath(hostPath string) error {
	hostPath = strings.TrimSpace(hostPath)
	if hostPath == "" {
		return fmt.Errorf("path is required")
	}
	if _, err := os.Stat(hostPath); err != nil {
		return fmt.Errorf("%s: %w", hostPath, err)
	}
	executable, args, err := hostOpenCommand(runtime.GOOS, hostPath)
	if err != nil {
		return err
	}
	return a.deps.launchHostOpener(executable, args)
}

// RevealHostPath opens the host file manager focused on the given path (or
// its containing directory, on platforms with no file-select primitive).
func (a *App) RevealHostPath(hostPath string) error {
	hostPath = strings.TrimSpace(hostPath)
	if hostPath == "" {
		return fmt.Errorf("path is required")
	}
	if _, err := os.Stat(hostPath); err != nil {
		return fmt.Errorf("%s: %w", hostPath, err)
	}
	executable, args, err := hostRevealCommand(runtime.GOOS, hostPath)
	if err != nil {
		return err
	}
	return a.deps.launchHostOpener(executable, args)
}

// podPathResolution is what ResolveEnvironmentHostPath reports for a path
// printed inside an environment's pod-side terminal: whether it has a host
// equivalent this desktop can open, and if not, why not. A pod path must
// never be resolved against a same-named host file (erun-ui/AGENTS.md and
// issue #1354) -- "unresolved" is always the safe default.
type podPathResolution struct {
	// Kind is "mirror" (the env's synced worktree), "artifact" (the synced
	// outputs mirror), or "unresolved".
	Kind     string `json:"kind"`
	HostPath string `json:"hostPath,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ResolveEnvironmentHostPath maps a path an environment's pod-side terminal
// printed onto its host-side equivalent, when one exists: under the pod's
// outputs directory it resolves via the synced .erun-outputs mirror; under the
// pod's worktree root it resolves via the same host workspace OpenHostIDE and
// LoadHostDiff already use (the local-agent worktree itself, or the
// remote-agent workspace-sync mirror). Anything else -- including an
// environment with no host workspace at all -- comes back unresolved with a
// stated reason rather than guessing at a same-named host file.
func (a *App) ResolveEnvironmentHostPath(selection uiSelection, podPath string) (podPathResolution, error) {
	selection = normalizeSelection(selection)
	podPath = strings.TrimSpace(podPath)
	if selection.Tenant == "" || selection.Environment == "" {
		return podPathResolution{}, fmt.Errorf("tenant and environment are required")
	}
	if podPath == "" {
		return podPathResolution{}, fmt.Errorf("path is required")
	}
	result, hostRoot, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return podPathResolution{Kind: "unresolved", Reason: err.Error()}, nil
	}
	cleanPodPath := path.Clean(filepath.ToSlash(podPath))
	if rel, ok := relUnderPodDir(cleanPodPath, eruncommon.DefaultRuntimeOutputsDir); ok {
		return resolveHostMirrorPath(
			"artifact",
			filepath.Join(hostRoot, eruncommon.WorkspaceSyncArtifactsSubdir, filepath.FromSlash(rel)),
			"no matching file has synced to this environment's outputs yet",
		), nil
	}
	podRoot := eruncommon.RemoteShellWorktreePath(eruncommon.ShellLaunchParamsFromResult(result))
	if rel, ok := relUnderPodDir(cleanPodPath, podRoot); ok {
		return resolveHostMirrorPath(
			"mirror",
			filepath.Join(hostRoot, filepath.FromSlash(rel)),
			"this path has not synced to the host workspace yet",
		), nil
	}
	return podPathResolution{
		Kind:   "unresolved",
		Reason: "this path is inside the environment's pod and has no host equivalent",
	}, nil
}

// relUnderPodDir reports the slash-separated path of podPath relative to dir
// (POSIX join rules, since pod paths are always Linux) when podPath falls
// under dir, and whether it does.
func relUnderPodDir(podPath, dir string) (string, bool) {
	dir = path.Clean(dir)
	if podPath == dir {
		return "", true
	}
	prefix := dir + "/"
	if !strings.HasPrefix(podPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(podPath, prefix), true
}

func resolveHostMirrorPath(kind, hostPath, missingReason string) podPathResolution {
	if _, err := os.Stat(hostPath); err != nil {
		return podPathResolution{Kind: "unresolved", Reason: missingReason}
	}
	return podPathResolution{Kind: kind, HostPath: hostPath}
}
