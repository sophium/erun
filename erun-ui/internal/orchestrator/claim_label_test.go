package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// claimLabelRunner records gh commands instead of running them.
type claimLabelRunner struct {
	labels []string
	calls  [][]string
	dirs   []string
	err    error
}

func (r *claimLabelRunner) run(_ context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	r.dirs = append(r.dirs, dir)
	if r.err != nil {
		return "", r.err
	}
	if len(args) >= 2 && args[0] == "label" && args[1] == "list" {
		return strings.Join(r.labels, "\n"), nil
	}
	return "", nil
}

func (r *claimLabelRunner) createCalls() [][]string {
	var creates [][]string
	for _, call := range r.calls {
		if len(call) >= 3 && call[1] == "label" && call[2] == "create" {
			creates = append(creates, call)
		}
	}
	return creates
}

// stubOrchestratorClaimLabel keeps tests from minting real labels via gh.
func stubOrchestratorClaimLabel(context.Context, string, string, ...string) (string, error) {
	return "", nil
}

func claimLabelApp(t *testing.T, runner workingIssueCommandRunner) *testApp {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	app := newTestApp(testDeps{
		store:                       newOrchestratorStubStore(t.TempDir()),
		runOrchestratorLabelCommand: runner,
	})
	return app
}

func TestOrchestratorProvisioningMintsTheClaimLabel(t *testing.T) {
	runner := &claimLabelRunner{labels: []string{"bug", "enhancement", "wip:erun"}}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspaceFor("erun-ideas")
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor failed: %v", err)
	}

	creates := runner.createCalls()
	if len(creates) != 1 {
		t.Fatalf("expected exactly one label creation, got %d: %v", len(creates), runner.calls)
	}
	want := []string{
		"gh", "label", "create", "wip:erun-ideas",
		"--repo", "sophium/erun",
		"--color", "FBCA04",
		"--description", "Claimed by orchestrator erun-ideas. Do not start this issue; see AGENTS.md / erun-orchestrate.",
	}
	if !reflect.DeepEqual(creates[0], want) {
		t.Fatalf("claim label creation\n got %q\nwant %q", creates[0], want)
	}
	for _, callDir := range runner.dirs {
		if callDir != dir {
			t.Fatalf("label command ran in %q, want the orchestrators root %q", callDir, dir)
		}
	}
}

func TestOrchestratorProvisioningLeavesAnExistingClaimLabelAlone(t *testing.T) {
	runner := &claimLabelRunner{labels: []string{"bug", "wip:erun-ideas", "wip:erun"}}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspaceFor("erun-ideas"); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor failed: %v", err)
	}

	if len(runner.calls) == 0 {
		t.Fatal("expected the claim label to be looked up before anything was created")
	}
	if creates := runner.createCalls(); len(creates) != 0 {
		t.Fatalf("an id that already has its label must not be touched, got %v", creates)
	}
}

func TestOrchestratorClaimLabelProbeMatchesWholeNames(t *testing.T) {
	runner := &claimLabelRunner{labels: []string{"wip:erun"}}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspaceFor("erun-ideas"); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor failed: %v", err)
	}

	creates := runner.createCalls()
	if len(creates) != 1 || creates[0][3] != "wip:erun-ideas" {
		t.Fatalf("expected wip:erun-ideas to be created, got %v", runner.calls)
	}
}

func TestOrchestratorClaimLabelFailureIsReportedNotGuessedAt(t *testing.T) {
	runner := &claimLabelRunner{err: errors.New("gh: command not found")}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspaceFor("erun-ideas")
	if err != nil {
		t.Fatalf("a failed claim-label step must not stop provisioning: %v", err)
	}
	if dir == "" {
		t.Fatal("expected the orchestrators workspace even when the label step failed")
	}
	if creates := runner.createCalls(); len(creates) != 0 {
		t.Fatalf("a probe that failed must not become a creation attempt, got %v", creates)
	}
}

func TestOrchestratorClaimLabelSkippedWithoutAnID(t *testing.T) {
	runner := &claimLabelRunner{}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspaceFor("   "); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor failed: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no label command for an unnamed session, got %v", runner.calls)
	}
}

func TestOrchestratorClaimLabelRefusesAnIDThatCannotNameOne(t *testing.T) {
	runner := &claimLabelRunner{}
	app := claimLabelApp(t, runner.run)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspaceFor("erun ideas"); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor failed: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no label command for an unusable id, got %v", runner.calls)
	}
}
