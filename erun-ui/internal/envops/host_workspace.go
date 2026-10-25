package envops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A host workspace is the local-agent worktree or the remote-agent sync mirror;
// it lets host-side actions skip the pod, which matters on Windows.

func hostWorkspacePath(result config.OpenResult, findProjectRoot seam.ProjectFinderFunc) string {
	if !result.RemoteRepo() {
		return strings.TrimSpace(result.RepoPath)
	}
	if result.EnvConfig.SSHD.Enabled && result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return workspace.WorkspaceSyncLocalPath(result, findProjectRoot)
	}
	return ""
}

// ResolveHostWorkspace returns the environment's host workspace, or an error
// naming how to get one.
func (s *Service) ResolveHostWorkspace(selection uimodel.Selection) (config.OpenResult, string, error) {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return config.OpenResult{}, "", err
	}
	path := hostWorkspacePath(result, s.d.FindProjectRoot)
	if strings.TrimSpace(path) == "" {
		return result, "", fmt.Errorf("environment %s/%s has no host workspace; enable workspace sync so its worktree mirrors to this machine", selection.Tenant, selection.Environment)
	}
	return result, path, nil
}

// LoadDirectoryDiff diffs a directory that belongs to no environment, with no
// env resolution or MCP. The path is validated here so errors name it.
func (s *Service) LoadDirectoryDiff(directory string, options uimodel.DiffOptions) (model.DiffResult, error) {
	path := strings.TrimSpace(directory)
	if path == "" {
		return model.DiffResult{}, fmt.Errorf("load directory diff: no directory given")
	}
	info, err := os.Stat(path)
	if err != nil {
		return model.DiffResult{}, fmt.Errorf("load directory diff %s: %w", path, err)
	}
	if !info.IsDir() {
		return model.DiffResult{}, fmt.Errorf("load directory diff %s: not a directory", path)
	}
	return lifecycle.ResolveGitDiffWithOptions(path, model.DiffOptions{
		Scope:          strings.TrimSpace(options.Scope),
		SelectedCommit: strings.TrimSpace(options.SelectedCommit),
		Target:         lifecycle.DiffTargetEnv,
	}, nil)
}

// LoadHostDiff diffs the env's host workspace with host git, never dialing the
// in-pod MCP, so it works while the pod is stopped.
func (s *Service) LoadHostDiff(selection uimodel.Selection, options uimodel.DiffOptions) (model.DiffResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("load host diff", selection.Tenant, selection.Environment); err != nil {
		return model.DiffResult{}, err
	}
	_, path, err := s.ResolveHostWorkspace(selection)
	if err != nil {
		return model.DiffResult{}, err
	}
	return lifecycle.ResolveGitDiffWithOptions(path, model.DiffOptions{
		Scope:          strings.TrimSpace(options.Scope),
		SelectedCommit: strings.TrimSpace(options.SelectedCommit),
		Target:         lifecycle.DiffTargetEnv,
	}, nil)
}

// OpenHostIDE launches VS Code or IntelliJ against the env's host workspace.
func (s *Service) OpenHostIDE(selection uimodel.Selection, ide string) error {
	selection = uimodel.NormalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if err := uimodel.ErrMissingTenantOrEnvironment("open host IDE", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	_, path, err := s.ResolveHostWorkspace(selection)
	if err != nil {
		return err
	}
	return s.d.OpenHostIDE(ide, path)
}

type HostArtifact struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// ListHostArtifacts lists artifacts sync mirrored from the pod's outputs dir.
func (s *Service) ListHostArtifacts(selection uimodel.Selection) ([]HostArtifact, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list host artifacts", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	_, path, err := s.ResolveHostWorkspace(selection)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(path, workspace.WorkspaceSyncArtifactsSubdir)
	rels, err := workspace.ListLocalArtifactFiles(dir)
	if err != nil {
		return nil, err
	}
	artifacts := make([]HostArtifact, 0, len(rels))
	for _, rel := range rels {
		info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if statErr != nil {
			continue
		}
		artifacts = append(artifacts, HostArtifact{
			Name:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return artifacts, nil
}

// RunHostArtifact launches a synced artifact on the host, detached; relPath
// must stay within the artifact directory.
func (s *Service) RunHostArtifact(selection uimodel.Selection, relPath string) error {
	selection = uimodel.NormalizeSelection(selection)
	relPath = strings.TrimSpace(relPath)
	if err := uimodel.ErrMissingTenantOrEnvironment("run host artifact", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	if !workspace.SafeWorkspaceSyncPath(relPath) {
		return fmt.Errorf("invalid artifact path %q", relPath)
	}
	_, path, err := s.ResolveHostWorkspace(selection)
	if err != nil {
		return err
	}
	dir := filepath.Join(path, workspace.WorkspaceSyncArtifactsSubdir)
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
	// An artifact may predate sync-side signing, so re-check at launch.
	s.reportHostArtifactSigning(codesign.SignHostArtifact(exePath))
	return s.d.LaunchHostArtifact(exePath, dir)
}
