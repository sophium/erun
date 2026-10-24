package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// erun rewrites the shared CLAUDE.md every launch; the role file must survive.
func TestOrchestratorRoleFileIsSeededOnceAndNeverRewritten(t *testing.T) {
	dir := t.TempDir()

	if err := ensureOrchestratorRoleFile(dir, "erun"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(dir, "CLAUDE.erun.md")
	seeded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("role file was not created: %v", err)
	}
	if len(seeded) == 0 {
		t.Fatal("role file seeded empty")
	}

	operator := []byte("# my standing role\nOnly ever open issues; never write code.\n")
	if err := os.WriteFile(path, operator, 0o644); err != nil {
		t.Fatalf("simulate an operator edit: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := ensureOrchestratorRoleFile(dir, "erun"); err != nil {
			t.Fatalf("re-seed pass %d: %v", i, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after re-seeding: %v", err)
	}
	if string(after) != string(operator) {
		t.Fatalf("the operator's role file was rewritten by a later launch:\n%s", after)
	}
}

// Scope comes from erun's config, never from disk.
func TestOrchestratorRoleFileSeedDoesNotBakeInScope(t *testing.T) {
	if !strings.Contains(orchestratorRoleFileSeed, "Do NOT list this orchestrator's environments") {
		t.Error("the seed must tell the operator not to bake an environment list into it")
	}
	if !strings.Contains(orchestratorRoleFileSeed, "never overwrites it") {
		t.Error("the seed must say erun will not overwrite the file, or the operator cannot trust it")
	}
}

// A transient session's empty id would seed "CLAUDE..md" in the shared root.
func TestOrchestratorRoleFileSkipsATransientSession(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "   "} {
		if err := ensureOrchestratorRoleFile(dir, id); err != nil {
			t.Fatalf("empty id must be a no-op, got %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("an empty id seeded %v; it must seed nothing", names)
	}
}

// The ordering is the precedence rule.
func TestOrchestratorSkillHookInjectsTheRoleFileAfterTheSharedContract(t *testing.T) {
	command := orchestratorSkillHookCommand("/tmp/orchestrators")

	shared := strings.Index(command, "CLAUDE.md")
	role := strings.Index(command, `CLAUDE."+id+".md`)
	if shared < 0 {
		t.Fatalf("hook does not read the shared contract:\n%s", command)
	}
	if role < 0 {
		t.Fatalf("hook does not read the per-orchestrator role file:\n%s", command)
	}
	if role < shared {
		t.Errorf("the role file is read BEFORE the shared contract, inverting precedence:\n%s", command)
	}
	if !strings.Contains(command, "if(id)") {
		t.Errorf("no guard on the id, so a transient session would read CLAUDE..md:\n%s", command)
	}
	// A missing role file is common and must not fail the hook or write to stderr.
	if !strings.Contains(command, `id+".md","utf8"));}catch(e){}`) {
		t.Errorf("a missing role file must be a silent no-op:\n%s", command)
	}
	if !orchestratorHookCommandIsPortable(command) {
		t.Errorf("the hook must run through node, not a POSIX shell: %q", command)
	}
}
