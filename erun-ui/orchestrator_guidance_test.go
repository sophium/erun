package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createTestOrchestrator links a local-agent env to avoid workspace-sync wiring.
func createTestOrchestrator(t *testing.T, app *App) string {
	t.Helper()
	info, err := app.CreateOrchestrator("laptop agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "laptop"},
	}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	return info.ID
}

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

func TestRevealOrchestratorGuidanceRejectsUnknownID(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if err := app.RevealOrchestratorGuidance("no-such-orchestrator", "role", "vscode"); err == nil {
		t.Fatal("expected an unknown orchestrator id to be rejected")
	}
}

func TestRevealOrchestratorGuidanceRejectsUnknownLayer(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createTestOrchestrator(t, app)

	if err := app.RevealOrchestratorGuidance(id, "bogus", "vscode"); err == nil {
		t.Fatal("expected an unknown guidance layer to be rejected")
	}
}

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
