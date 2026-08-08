package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// A host workspace is the operator's host-machine copy of an env's worktree. For
// a local-agent env that is the worktree itself (RepoPath); for a remote-agent
// env it is the read-only mirror that workspace sync maintains on the host. Both
// let the desktop run host-side actions — diff, IDE, running a cross-built
// artifact — without reaching into the pod, which is what makes the flow usable
// on Windows where the local-agent hostPath mount is not.

// hostWorkspacePath returns the env's host-accessible worktree path, or "" when
// the env has none (a remote-agent without workspace sync, or a runtime env).
func hostWorkspacePath(result eruncommon.OpenResult, findProjectRoot eruncommon.ProjectFinderFunc) string {
	if !result.RemoteRepo() {
		return strings.TrimSpace(result.RepoPath)
	}
	if result.EnvConfig.SSHD.Enabled && result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return eruncommon.WorkspaceSyncLocalPath(result, findProjectRoot)
	}
	return ""
}

func (a *App) resolveHostWorkspace(selection uiSelection) (eruncommon.OpenResult, string, error) {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.OpenResult{}, "", err
	}
	path := hostWorkspacePath(result, a.deps.findProjectRoot)
	if strings.TrimSpace(path) == "" {
		return result, "", fmt.Errorf("environment %s/%s has no host workspace; enable workspace sync so its worktree mirrors to this machine", selection.Tenant, selection.Environment)
	}
	return result, path, nil
}

// LoadHostDiff computes the env's diff from its host workspace using host git,
// reusing the same shared resolver that backs `erun diff` and the MCP diff tool.
// Unlike LoadDiff it never dials the in-pod MCP, so it works for a remote-agent
// env whose runtime pod is stopped or unforwarded — the review reads the synced
// mirror directly.
func (a *App) LoadHostDiff(selection uiSelection, options uiDiffOptions) (eruncommon.DiffResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return eruncommon.DiffResult{}, fmt.Errorf("tenant and environment are required")
	}
	_, path, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return eruncommon.DiffResult{}, err
	}
	// The host mirror is the env worktree; the contribute-clone target has no
	// host-side meaning, so the host diff is always scoped to the env worktree.
	return eruncommon.ResolveGitDiffWithOptions(path, eruncommon.DiffOptions{
		Scope:          strings.TrimSpace(options.Scope),
		SelectedCommit: strings.TrimSpace(options.SelectedCommit),
		Target:         eruncommon.DiffTargetEnv,
	}, nil)
}

// OpenHostIDE launches VS Code or IntelliJ against the env's host workspace
// (the local-agent worktree or the remote-agent synced mirror), never the
// in-pod copy. The in-pod IDE path stays on OpenIDE for envs that want it.
func (a *App) OpenHostIDE(selection uiSelection, ide string) error {
	selection = normalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if selection.Tenant == "" || selection.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	_, path, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return err
	}
	executable, args, err := localOpenIDECommand(runtime.GOOS, ide, path)
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, startTerminalSessionParams{
		Dir:        path,
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
	if err == nil {
		return nil
	}
	return formatOpenIDEError(ide, output, err)
}

// hostArtifact describes one deliverable the outputs sync lane mirrored into the
// host workspace's read-only artifact directory.
type hostArtifact struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// ListHostArtifacts lists the artifacts workspace sync mirrored from the pod's
// $ERUN_OUTPUTS_DIR into the host workspace (e.g. a cross-built Windows .exe).
func (a *App) ListHostArtifacts(selection uiSelection) ([]hostArtifact, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	_, path, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(path, eruncommon.WorkspaceSyncArtifactsSubdir)
	rels, err := eruncommon.ListLocalArtifactFiles(dir)
	if err != nil {
		return nil, err
	}
	artifacts := make([]hostArtifact, 0, len(rels))
	for _, rel := range rels {
		info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if statErr != nil {
			continue
		}
		artifacts = append(artifacts, hostArtifact{
			Name:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return artifacts, nil
}

// RunHostArtifact launches a synced artifact on the host, detached, so the
// operator can run or debug a binary the agent cross-built in the pod. relPath
// is validated to stay within the workspace's read-only artifact directory.
func (a *App) RunHostArtifact(selection uiSelection, relPath string) error {
	selection = normalizeSelection(selection)
	relPath = strings.TrimSpace(relPath)
	if selection.Tenant == "" || selection.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	if !eruncommon.SafeWorkspaceSyncPath(relPath) {
		return fmt.Errorf("invalid artifact path %q", relPath)
	}
	_, path, err := a.resolveHostWorkspace(selection)
	if err != nil {
		return err
	}
	dir := filepath.Join(path, eruncommon.WorkspaceSyncArtifactsSubdir)
	exePath := filepath.Join(dir, filepath.FromSlash(relPath))
	rel, relErr := filepath.Rel(dir, exePath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path escapes the workspace outputs directory")
	}
	info, statErr := os.Stat(exePath)
	if statErr != nil {
		return fmt.Errorf("artifact %s: %w", relPath, statErr)
	}
	if info.IsDir() {
		return fmt.Errorf("artifact %q is a directory", relPath)
	}
	return a.deps.launchHostArtifact(exePath, dir)
}
