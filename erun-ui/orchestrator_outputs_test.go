package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withOrchestratorConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	// macOS resolves UserConfigDir from HOME rather than XDG_CONFIG_HOME.
	t.Setenv("HOME", home)
	return home
}

// Each orchestrator gets its own directory: they share one workspace, so a
// shared outputs directory would show every orchestrator every other one's
// files.
func TestOrchestratorOutputsDirIsPerOrchestrator(t *testing.T) {
	withOrchestratorConfigHome(t)

	first, err := orchestratorOutputsDir("erun")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := orchestratorOutputsDir("petios")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if first == second {
		t.Fatalf("two orchestrators must not share an outputs directory: %q", first)
	}
	if _, err := orchestratorOutputsDir("  "); err == nil {
		t.Fatal("an empty orchestrator id has no directory")
	}
	// The id reaches a filesystem path, so it must not be able to steer it.
	if _, err := orchestratorOutputsDir("../elsewhere"); err == nil {
		t.Fatal("an id that is not a single path segment must be refused")
	}
}

// The dialog reads this list, so a not-yet-used orchestrator has to report an
// empty directory rather than an error — "nothing here yet" is the normal
// starting state, not a fault.
func TestListOrchestratorOutputsReportsNewestFirstAndToleratesNothingYet(t *testing.T) {
	withOrchestratorConfigHome(t)
	app := &App{}

	listed, err := app.ListOrchestratorOutputs("erun")
	if err != nil {
		t.Fatalf("an unused orchestrator must not error: %v", err)
	}
	if len(listed.Entries) != 0 {
		t.Fatalf("expected no entries yet, got %+v", listed.Entries)
	}

	dir, err := ensureOrchestratorOutputsDir("erun")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	older := filepath.Join(dir, "older.txt")
	newer := filepath.Join(dir, "newer.txt")
	for _, path := range []string{older, newer} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	listed, err = app.ListOrchestratorOutputs("erun")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Entries) != 2 || listed.Entries[0].Name != "newer.txt" {
		t.Fatalf("expected newest-first, got %+v", listed.Entries)
	}
}

// The name reaches the filesystem, so an entry that climbs out of the outputs
// directory has to be refused rather than resolved — the dialog must not become
// a way to read or run the rest of the host.
func TestOrchestratorOutputsRefuseNamesOutsideTheDirectory(t *testing.T) {
	withOrchestratorConfigHome(t)
	app := &App{}
	if _, err := ensureOrchestratorOutputsDir("erun"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	for _, name := range []string{"../escape.txt", "nested/child.txt", ""} {
		if err := app.RunOrchestratorOutputOnHost("erun", name); err == nil {
			t.Fatalf("expected %q to be refused", name)
		}
	}
	if _, err := app.DownloadOrchestratorOutput("erun", "../escape.txt"); err == nil {
		t.Fatal("expected a climbing name to be refused before any save dialog")
	}
}

// Running an output is running a host-native binary the orchestrator produced,
// so it goes through the same launcher the environment artifacts use.
func TestRunOrchestratorOutputOnHostLaunchesTheProducedFile(t *testing.T) {
	withOrchestratorConfigHome(t)
	dir, err := ensureOrchestratorOutputsDir("erun")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	artifact := filepath.Join(dir, "report.sh")
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	var launched, launchedDir string
	app := &App{deps: erunUIDeps{launchHostArtifact: func(path, workdir string) error {
		launched, launchedDir = path, workdir
		return nil
	}}}

	if err := app.RunOrchestratorOutputOnHost("erun", "report.sh"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if launched != artifact {
		t.Fatalf("launched %q, want %q", launched, artifact)
	}
	if !strings.HasSuffix(launchedDir, filepath.Join("orchestrator-outputs", "erun")) {
		t.Fatalf("working dir %q must be the orchestrator's outputs directory", launchedDir)
	}
}
