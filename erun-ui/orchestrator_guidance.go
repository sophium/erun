package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// The role layer (CLAUDE.<id>.md) is seeded once; the shared layer (CLAUDE.md) is
// rewritten on every launch.
const (
	orchestratorGuidanceLayerRole   = "role"
	orchestratorGuidanceLayerShared = "shared"
)

type orchestratorGuidancePaths struct {
	Role   string `json:"role"`
	Shared string `json:"shared"`
}

// orchestratorGuidanceFilePath uses an exact Join, never a glob, so a hand-edited
// id cannot widen what is opened.
func orchestratorGuidanceFilePath(dir, id, layer string) (string, error) {
	switch strings.TrimSpace(layer) {
	case orchestratorGuidanceLayerRole:
		return filepath.Join(dir, "CLAUDE."+id+".md"), nil
	case orchestratorGuidanceLayerShared:
		return filepath.Join(dir, "CLAUDE.md"), nil
	default:
		return "", fmt.Errorf("unknown orchestrator guidance layer %q", layer)
	}
}

// OrchestratorGuidancePaths resolves the guidance paths without creating or seeding anything.
func (a *App) OrchestratorGuidancePaths(id string) orchestratorGuidancePaths {
	id = strings.TrimSpace(id)
	dir := orchestratorsRoot()
	role, _ := orchestratorGuidanceFilePath(dir, id, orchestratorGuidanceLayerRole)
	shared, _ := orchestratorGuidanceFilePath(dir, id, orchestratorGuidanceLayerShared)
	return orchestratorGuidancePaths{Role: role, Shared: shared}
}

// RevealOrchestratorGuidance opens a guidance layer in the host IDE, seeding the
// role file first so it always exists.
func (a *App) RevealOrchestratorGuidance(id, layer, ide string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("orchestrator id is required")
	}
	if _, err := a.findOrchestratorConfig(id); err != nil {
		return err
	}
	ide = strings.TrimSpace(ide)
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	dir, err := a.ensureOrchestratorWorkspaceFor(id)
	if err != nil {
		return err
	}
	path, err := orchestratorGuidanceFilePath(dir, id, layer)
	if err != nil {
		return err
	}
	executable, args, err := terminal.LocalOpenIDECommand(runtime.GOOS, ide, path)
	if err != nil {
		return err
	}
	ctx := a.context()
	output, err := a.deps.runIDECommand(ctx, terminal.StartParams{
		Dir:        dir,
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
	if err == nil {
		return nil
	}
	return terminal.FormatOpenIDEError(ide, output, err)
}
