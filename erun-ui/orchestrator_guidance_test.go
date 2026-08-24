package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createTestOrchestrator persists a single orchestrator linking the laptop
// local-agent env, returning its id. Local-agent avoids the workspace-sync
// wiring a remote-agent link would otherwise pull in.
func createTestOrchestrator(t *testing.T, app *App) string {
	t.Helper()
	info, err := app.CreateOrchestrator("laptop agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "laptop"},
	})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	return info.ID
}

// TestRevealOrchestratorGuidanceResolvesBothLayers locks the mapping from
// layer name to file: "role" opens this orchestrator's own CLAUDE.<id>.md,
// "shared" opens the one CLAUDE.md every orchestrator obeys. Both live in the
// shared orchestrators root, never a per-orchestrator subfolder.
func TestRevealOrchestratorGuidanceResolvesBothLayers(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createTestOrchestrator(t, app)

	var opened []string
	app.deps.runIDECommand = func(_ context.Context, params startTerminalSessionParams) (string, error) {
		opened = append(opened, params.Args[len(params.Args)-1])
		return "", nil
	}

	if err := app.RevealOrchestratorGuidance(id, "role", "vscode"); err != nil {
		t.Fatalf("RevealOrchestratorGuidance role failed: %v", err)
	}
	if err := app.RevealOrchestratorGuidance(id, "shared", "vscode"); err != nil {
		t.Fatalf("RevealOrchestratorGuidance shared failed: %v", err)
	}

	dir := orchestratorsRoot()
	wantRole := filepath.Join(dir, "CLAUDE."+id+".md")
	wantShared := filepath.Join(dir, "CLAUDE.md")
	if len(opened) != 2 || opened[0] != wantRole || opened[1] != wantShared {
		t.Fatalf("opened = %+v, want [%q %q]", opened, wantRole, wantShared)
	}
}

// TestRevealOrchestratorGuidanceSeedsAbsentRoleFile covers the acceptance
// criterion from #1231: revealing an orchestrator's role layer before it has
// ever launched must seed the file rather than fail or open nothing, so the
// button never reveals a missing file.
func TestRevealOrchestratorGuidanceSeedsAbsentRoleFile(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createTestOrchestrator(t, app)

	rolePath := filepath.Join(orchestratorsRoot(), "CLAUDE."+id+".md")
	if _, err := os.Stat(rolePath); !os.IsNotExist(err) {
		t.Fatalf("expected no role file before the orchestrator has launched, stat err = %v", err)
	}

	app.deps.runIDECommand = func(context.Context, startTerminalSessionParams) (string, error) {
		return "", nil
	}
	if err := app.RevealOrchestratorGuidance(id, "role", "vscode"); err != nil {
		t.Fatalf("RevealOrchestratorGuidance failed: %v", err)
	}

	data, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("expected the role file to be seeded, read failed: %v", err)
	}
	if !strings.Contains(string(data), "This file is yours") {
		t.Fatalf("seeded role file missing its seed text:\n%s", data)
	}
}

// TestRevealOrchestratorGuidanceRejectsUnknownID protects the exact-Join
// contract: a hand-edited config.yaml id, or any id with no persisted
// definition, must be rejected rather than silently resolving a path.
func TestRevealOrchestratorGuidanceRejectsUnknownID(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if err := app.RevealOrchestratorGuidance("no-such-orchestrator", "role", "vscode"); err == nil {
		t.Fatal("expected an unknown orchestrator id to be rejected")
	}
}

// TestRevealOrchestratorGuidanceRejectsUnknownLayer covers the third input:
// an unrecognized layer name must fail loudly rather than default to one file.
func TestRevealOrchestratorGuidanceRejectsUnknownLayer(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createTestOrchestrator(t, app)

	if err := app.RevealOrchestratorGuidance(id, "bogus", "vscode"); err == nil {
		t.Fatal("expected an unknown guidance layer to be rejected")
	}
}

// TestOrchestratorGuidancePathsResolvesWithoutSeeding covers the read-only
// half the dialog uses to label each entry with its resolved path: it must not
// create the workspace or the role file as a side effect of merely displaying
// where they would live.
func TestOrchestratorGuidancePathsResolvesWithoutSeeding(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createTestOrchestrator(t, app)

	paths := app.OrchestratorGuidancePaths(id)
	dir := orchestratorsRoot()
	if paths.Role != filepath.Join(dir, "CLAUDE."+id+".md") {
		t.Fatalf("unexpected role path: %q", paths.Role)
	}
	if paths.Shared != filepath.Join(dir, "CLAUDE.md") {
		t.Fatalf("unexpected shared path: %q", paths.Shared)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected no workspace to be created by resolving paths, stat err = %v", err)
	}
}
