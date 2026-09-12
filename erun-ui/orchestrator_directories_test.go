package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A directory of its own is a complete orchestrator definition: no environment
// is linked, nothing is registered anywhere, and the orchestrator just works in
// that path. That is the configuration the directories field exists for, so it
// must not be refused for "linking no environment".
func TestCreateOrchestratorAcceptsDirectoriesWithoutEnvironments(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := t.TempDir()
	info, err := app.CreateOrchestrator("scratch", nil, []string{dir})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Environments) != 0 {
		t.Fatalf("expected no linked environments, got %+v", info.Environments)
	}
	if len(info.Directories) != 1 || info.Directories[0] != dir {
		t.Fatalf("expected the picked directory %q, got %+v", dir, info.Directories)
	}
	// Persisted, and still there when the definition is listed back.
	listed := app.ListOrchestrators()
	if len(listed) != 1 || len(listed[0].Directories) != 1 || listed[0].Directories[0] != dir {
		t.Fatalf("expected the directory to persist, got %+v", listed)
	}
}

// A directory that is not there, or is not absolute, is a link that cannot work:
// the orchestrator authors and builds in the path directly, so it is refused
// while the operator is still looking at the dialog rather than at first use.
func TestCreateOrchestratorRefusesAMissingOrRelativeDirectory(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	missing := filepath.Join(t.TempDir(), "not-there")
	if _, err := app.CreateOrchestrator("missing", nil, []string{missing}); err == nil {
		t.Fatal("expected a directory that does not exist to be refused")
	} else if !strings.Contains(err.Error(), missing) {
		t.Fatalf("refusal should name the path the operator picked, got %q", err)
	}
	if _, err := app.CreateOrchestrator("relative", nil, []string{"relative/path"}); err == nil {
		t.Fatal("expected a relative directory to be refused")
	}
}

// An orchestrator with neither a linked environment nor a directory has nothing
// to operate on, so it is still refused -- the rule widened, it did not go away.
func TestOrchestratorScopeNeedsAnEnvironmentOrADirectory(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.CreateOrchestrator("empty", nil, nil); err == nil {
		t.Fatal("expected an orchestrator with no scope at all to be refused")
	}
	if _, err := app.CreateOrchestrator("blank", nil, []string{"  "}); err == nil {
		t.Fatal("expected a blank directory entry to leave no scope, and be refused")
	}
}

// Adding the same directory twice is one directory: the dialog collapses it, and
// the backend must agree, or a saved definition could hold a duplicate row the
// form will not show.
func TestCreateOrchestratorCollapsesDuplicateDirectories(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := t.TempDir()
	info, err := app.CreateOrchestrator("dupe", nil, []string{dir, dir, ""})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Directories) != 1 || info.Directories[0] != dir {
		t.Fatalf("expected one collapsed directory %q, got %+v", dir, info.Directories)
	}
}

// Update replaces the directory set rather than accumulating it, and leaves the
// linked environments alone.
func TestUpdateOrchestratorReplacesDirectories(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	first, second := t.TempDir(), t.TempDir()
	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, []string{first})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	updated, err := app.UpdateOrchestrator(
		created.ID, "agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, []string{second})
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

// The injected contract is what tells a session these paths are its own, so the
// guidance has to name them. Pinned here because the text is the feature's only
// reach into a running session.
func TestOrchestratorClaudeMdTeachesItsOwnDirectories(t *testing.T) {
	for _, want := range []string{"directories:", "no environment at all", "nothing else owns them"} {
		if !strings.Contains(orchestratorClaudeMd, want) {
			t.Errorf("injected contract does not mention %q", want)
		}
	}
}
