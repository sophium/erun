package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// orchestratorGuidanceLayerRole and orchestratorGuidanceLayerShared are the two
// layers RevealOrchestratorGuidance and OrchestratorGuidancePaths resolve: the
// operator's own standing role (CLAUDE.<id>.md, seeded once and never
// rewritten) and the one contract every orchestrator obeys (CLAUDE.md,
// rewritten by erun on every launch). See orchestratorRoleFileSeed and
// orchestratorClaudeMd for what each one carries.
const (
	orchestratorGuidanceLayerRole   = "role"
	orchestratorGuidanceLayerShared = "shared"
)

// orchestratorGuidancePaths is the JSON-safe, resolved view of where an
// orchestrator's two guidance layers live on this host, so the desktop can
// show the operator the convention (and that `<id>` is not the display name)
// instead of a bare filename.
type orchestratorGuidancePaths struct {
	Role   string `json:"role"`
	Shared string `json:"shared"`
}

// orchestratorGuidanceFilePath maps a layer name to its file, resolved with an
// exact filepath.Join and never a glob, so a hand-edited config.yaml id cannot
// widen what gets read or opened.
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

// OrchestratorGuidancePaths resolves the two guidance layers for a persisted
// orchestrator, without creating the workspace or seeding anything —
// OrchestratorDialog calls it to label each entry with its resolved path.
func (a *App) OrchestratorGuidancePaths(id string) orchestratorGuidancePaths {
	id = strings.TrimSpace(id)
	dir := orchestratorsRoot()
	role, _ := orchestratorGuidanceFilePath(dir, id, orchestratorGuidanceLayerRole)
	shared, _ := orchestratorGuidanceFilePath(dir, id, orchestratorGuidanceLayerShared)
	return orchestratorGuidancePaths{Role: role, Shared: shared}
}

// RevealOrchestratorGuidance opens one of an orchestrator's two guidance layers
// in the operator's chosen host IDE — the same vscode/intellij choice
// OpenHostIDE already offers, so this introduces no second editor setting. It
// ensures the shared workspace and seeds this orchestrator's role file first
// (ensureOrchestratorWorkspaceFor), so revealing the role layer before the
// orchestrator has ever launched still opens a real file rather than nothing.
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
	executable, args, err := localOpenIDECommand(runtime.GOOS, ide, path)
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, startTerminalSessionParams{
		Dir:        dir,
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
	if err == nil {
		return nil
	}
	return formatOpenIDEError(ide, output, err)
}
