package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateOrchestratorAcceptsDirectoriesWithoutEnvironments(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := t.TempDir()
	info, err := app.CreateOrchestrator("scratch", nil, []string{dir}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Environments) != 0 {
		t.Fatalf("expected no linked environments, got %+v", info.Environments)
	}
	if len(info.Directories) != 1 || info.Directories[0] != dir {
		t.Fatalf("expected the picked directory %q, got %+v", dir, info.Directories)
	}
	listed := app.ListOrchestrators()
	if len(listed) != 1 || len(listed[0].Directories) != 1 || listed[0].Directories[0] != dir {
		t.Fatalf("expected the directory to persist, got %+v", listed)
	}
}

func TestCreateOrchestratorRefusesAMissingOrRelativeDirectory(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	missing := filepath.Join(t.TempDir(), "not-there")
	if _, err := app.CreateOrchestrator("missing", nil, []string{missing}, "", LaunchInput{}); err == nil {
		t.Fatal("expected a directory that does not exist to be refused")
	} else if !strings.Contains(err.Error(), missing) {
		t.Fatalf("refusal should name the path the operator picked, got %q", err)
	}
	if _, err := app.CreateOrchestrator("relative", nil, []string{"relative/path"}, "", LaunchInput{}); err == nil {
		t.Fatal("expected a relative directory to be refused")
	}
}

func TestOrchestratorScopeNeedsAnEnvironmentOrADirectory(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.CreateOrchestrator("empty", nil, nil, "", LaunchInput{}); err == nil {
		t.Fatal("expected an orchestrator with no scope at all to be refused")
	}
	if _, err := app.CreateOrchestrator("blank", nil, []string{"  "}, "", LaunchInput{}); err == nil {
		t.Fatal("expected a blank directory entry to leave no scope, and be refused")
	}
}

func TestCreateOrchestratorCollapsesDuplicateDirectories(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := t.TempDir()
	info, err := app.CreateOrchestrator("dupe", nil, []string{dir, dir, ""}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Directories) != 1 || info.Directories[0] != dir {
		t.Fatalf("expected one collapsed directory %q, got %+v", dir, info.Directories)
	}
}

func TestUpdateOrchestratorReplacesDirectories(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	first, second := t.TempDir(), t.TempDir()
	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, []string{first}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	updated, err := app.UpdateOrchestrator(
		created.ID, "agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, []string{second}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}
	if len(updated.Directories) != 1 || updated.Directories[0] != second {
		t.Fatalf("expected the directory set replaced with %q, got %+v", second, updated.Directories)
	}
	if len(updated.Environments) != 1 {
		t.Fatalf("expected the linked environment to survive the edit, got %+v", updated.Environments)
	}
}

func TestOrchestratorClaudeMdTeachesItsOwnDirectories(t *testing.T) {
	for _, want := range []string{"directories:", "no environment at all", "nothing else owns them"} {
		if !strings.Contains(orchestratorClaudeMd, want) {
			t.Errorf("injected contract does not mention %q", want)
		}
	}
}

func TestARunningOrchestratorStillReportsItsOwnDirectories(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := t.TempDir()
	created, err := app.CreateOrchestrator("scratch", nil, []string{dir}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if len(started.Directories) != 1 || started.Directories[0] != dir {
		t.Fatalf("a running orchestrator must report its own directories, got %+v", started.Directories)
	}
	listed := app.ListOrchestrators()
	if len(listed) != 1 || len(listed[0].Directories) != 1 || listed[0].Directories[0] != dir {
		t.Fatalf("a running orchestrator's directories must survive into the list, got %+v", listed)
	}
}

func TestUpdateOrchestratorAddsADirectoryWhileRunning(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	dir := t.TempDir()
	updated, err := app.UpdateOrchestrator(
		created.ID, "agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, []string{dir}, "", LaunchInput{})
	if err != nil {
		t.Fatalf("UpdateOrchestrator while running failed: %v", err)
	}
	if len(updated.Directories) != 1 || updated.Directories[0] != dir {
		t.Fatalf("expected the added directory on a running orchestrator, got %+v", updated.Directories)
	}
}

func TestAJustSavedDirectoryIsReportedByARunningOrchestrator(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	dir := t.TempDir()
	if _, err := app.UpdateOrchestrator(
		created.ID, "agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, []string{dir}, "", LaunchInput{}); err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}

	listed := app.ListOrchestrators()
	if len(listed) != 1 {
		t.Fatalf("expected one orchestrator, got %+v", listed)
	}
	if len(listed[0].Directories) != 1 || listed[0].Directories[0] != dir {
		t.Fatalf("a directory saved while running must be reported back, got %+v", listed[0].Directories)
	}
}
