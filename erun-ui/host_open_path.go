package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
)

// hostOpenCommand passes the path as a discrete exec argument, never
// shell-interpolated: it can come from untrusted terminal output.
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

// hostRevealCommand opens the parent directory on Linux, which has no portable
// file-select primitive.
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

// Release lets the opener outlive this process.
func launchHostOpenerDetached(executable string, args []string) error {
	cmd := exec.Command(executable, args...)
	core.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", executable, err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

// OpenHostPath opens a host path with the OS handler; pod paths must go through
// ResolveEnvironmentHostPath first.
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

// RevealHostPath opens the host file manager focused on the given path.
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

// A pod path must never resolve to a same-named host file; "unresolved" is the
// safe default.
type podPathResolution struct {
	// Kind is "mirror", "artifact", or "unresolved".
	Kind     string `json:"kind"`
	HostPath string `json:"hostPath,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ResolveEnvironmentHostPath maps a pod-side terminal path onto its host
// equivalent via the outputs mirror or host workspace, else unresolved.
func (a *App) ResolveEnvironmentHostPath(selection uiSelection, podPath string) (podPathResolution, error) {
	selection = normalizeSelection(selection)
	podPath = strings.TrimSpace(podPath)
	if err := errMissingTenantOrEnvironment("resolve environment host path", selection.Tenant, selection.Environment); err != nil {
		return podPathResolution{}, err
	}
	if podPath == "" {
		return podPathResolution{}, fmt.Errorf("path is required")
	}
	result, hostRoot, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return podPathResolution{Kind: "unresolved", Reason: fmt.Sprint(err)}, nil
	}
	cleanPodPath := path.Clean(filepath.ToSlash(podPath))
	if rel, ok := relUnderPodDir(cleanPodPath, core.DefaultRuntimeOutputsDir); ok {
		return resolveHostMirrorPath(
			"artifact",
			filepath.Join(hostRoot, workspace.WorkspaceSyncArtifactsSubdir, filepath.FromSlash(rel)),
			"no matching file has synced to this environment's outputs yet",
		), nil
	}
	podRoot := runtimeref.RemoteShellWorktreePath(cloud.ShellLaunchParamsFromResult(result))
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

// Pod paths are always Linux, so POSIX rules apply.
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
