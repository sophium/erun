package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// orchestratorOutputsDir is where one orchestrator's deliverables land on this
// host. An orchestrator has no pod, so the outputs convention an in-pod agent
// already follows — write to $ERUN_OUTPUTS_DIR — needs a host directory to point
// at. It is per-orchestrator and sits beside the other per-orchestrator state,
// so two orchestrators never read each other's files.
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

// ensureOrchestratorOutputsDir creates the directory before the session starts,
// so an agent told to write there finds it rather than having to create it and
// guess the convention.
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

// ListOrchestratorOutputs lists what an orchestrator produced on this host,
// newest-first. It reads the filesystem directly — there is no pod to exec into
// — through the same resolver the in-pod MCP server uses, so the read-model the
// dialog renders is identical for both.
func (a *App) ListOrchestratorOutputs(id string) (eruncommon.RuntimeOutputsListResult, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return eruncommon.RuntimeOutputsListResult{}, err
	}
	return eruncommon.ResolveLocalOutputs(eruncommon.RuntimeOutputsParams{Dir: dir})
}

// DownloadOrchestratorOutput saves one entry through a native Save dialog and
// returns the saved path, or an empty string when the operator cancels. A folder
// saves as a <name>.tar.gz, matching the environment flow.
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
	out, err := eruncommon.DownloadLocalOutput(eruncommon.RuntimeOutputDownloadParams{Dir: dir, Name: name})
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
		return "", nil // operator cancelled the dialog
	}
	if err := os.WriteFile(dest, out.Bytes, 0o644); err != nil {
		return "", err
	}
	// The download already signed the source, and an ad-hoc signature travels
	// inside the Mach-O — but the saved copy still lands 0644, so it needs the
	// execute bit this seam applies.
	a.reportHostArtifactSigning(eruncommon.SignHostArtifact(dest))
	return dest, nil
}

// RunOrchestratorOutputOnHost runs one produced file here. An orchestrator's
// outputs are already host-native — it produced them on this machine — so this
// is the run an environment's artifacts need a mirror to reach.
func (a *App) RunOrchestratorOutputOnHost(id, name string) error {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return err
	}
	// Reuse the download resolver's containment check rather than re-deriving
	// it: it already refuses a name that climbs out of the outputs directory.
	entry, err := eruncommon.StatLocalOutput(eruncommon.RuntimeOutputDownloadParams{Dir: dir, Name: name})
	if err != nil {
		return err
	}
	if entry.IsArchive {
		return fmt.Errorf("output %q is a directory", name)
	}
	exePath := filepath.Join(dir, entry.Name)
	a.reportHostArtifactSigning(eruncommon.SignHostArtifact(exePath))
	return a.deps.launchHostArtifact(exePath, dir)
}
