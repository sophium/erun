package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// orchestratorOutputsDir is the per-orchestrator host directory $ERUN_OUTPUTS_DIR
// points at; an orchestrator has no pod.
func orchestratorOutputsDir(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("an orchestrator id is required")
	}
	if id != filepath.Base(id) || id == "." || id == ".." {
		return "", fmt.Errorf("orchestrator id %q is not a single path segment", id)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-outputs", id), nil
}

func ensureOrchestratorOutputsDir(id string) (string, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ListOrchestratorOutputs lists outputs newest-first via the in-pod MCP server's resolver.
func (a *App) ListOrchestratorOutputs(id string) (jobs.RuntimeOutputsListResult, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return jobs.RuntimeOutputsListResult{}, err
	}
	return jobs.ResolveLocalOutputs(jobs.RuntimeOutputsParams{Dir: dir})
}

// DownloadOrchestratorOutput saves one entry via a Save dialog (a folder as
// <name>.tar.gz); "" means cancelled.
func (a *App) DownloadOrchestratorOutput(id, name string) (string, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	out, err := jobs.DownloadLocalOutput(jobs.RuntimeOutputDownloadParams{Dir: dir, Name: name})
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save orchestrator output",
		DefaultFilename: out.Name,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dest) == "" {
		return "", nil
	}
	if err := os.WriteFile(dest, out.Bytes, 0o644); err != nil {
		return "", err
	}
	// The saved copy lands 0644, so it still needs the execute bit.
	a.reportHostArtifactSigning(codesign.SignHostArtifact(dest))
	return dest, nil
}

// RunOrchestratorOutputOnHost runs one produced, already host-native file.
func (a *App) RunOrchestratorOutputOnHost(id, name string) error {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return err
	}
	// StatLocalOutput refuses a name that climbs out of the outputs directory.
	entry, err := jobs.StatLocalOutput(jobs.RuntimeOutputDownloadParams{Dir: dir, Name: name})
	if err != nil {
		return err
	}
	if entry.IsArchive {
		return fmt.Errorf("output %q is a directory", name)
	}
	exePath := filepath.Join(dir, entry.Name)
	a.reportHostArtifactSigning(codesign.SignHostArtifact(exePath))
	return a.deps.launchHostArtifact(exePath, dir)
}
