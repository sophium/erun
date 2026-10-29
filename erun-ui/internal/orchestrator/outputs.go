package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/codesign"
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
func (s *Service) ListOrchestratorOutputs(id string) (model.RuntimeOutputsListResult, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return model.RuntimeOutputsListResult{}, err
	}
	return jobs.ResolveLocalOutputs(model.RuntimeOutputsParams{Dir: dir})
}

// DownloadOrchestratorOutput saves one entry where saveAs, the native Save
// dialog, chooses (a folder as <name>.tar.gz); "" means cancelled.
func (s *Service) DownloadOrchestratorOutput(id, name string, saveAs func(defaultName string) (string, error)) (string, error) {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if !s.d.Started() {
		return "", fmt.Errorf("application context is not ready")
	}
	out, err := jobs.DownloadLocalOutput(model.RuntimeOutputDownloadParams{Dir: dir, Name: name})
	if err != nil {
		return "", err
	}
	dest, err := saveAs(out.Name)
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
	s.d.ReportHostArtifactSigning(codesign.SignHostArtifact(dest))
	return dest, nil
}

// RunOrchestratorOutputOnHost runs one produced, already host-native file.
func (s *Service) RunOrchestratorOutputOnHost(id, name string) error {
	dir, err := orchestratorOutputsDir(id)
	if err != nil {
		return err
	}
	// StatLocalOutput refuses a name that climbs out of the outputs directory.
	entry, err := jobs.StatLocalOutput(model.RuntimeOutputDownloadParams{Dir: dir, Name: name})
	if err != nil {
		return err
	}
	if entry.IsArchive {
		return fmt.Errorf("output %q is a directory", name)
	}
	exePath := filepath.Join(dir, entry.Name)
	s.d.ReportHostArtifactSigning(codesign.SignHostArtifact(exePath))
	return s.d.LaunchHostArtifact(exePath, dir)
}
