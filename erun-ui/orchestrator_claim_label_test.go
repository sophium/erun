package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// claimLabelRunner is the GitHub boundary the provisioning path reaches,
// recorded rather than crossed: it answers the label-existence probe from a
// fixed label set and captures every command it was asked to run, so a test can
// see both that a missing label is created and that an id which already has one
// is left alone. No test here may reach a real repository.
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

// createCalls returns the label-creating commands, which is the only call the
// provisioning path makes that changes anything.
func (r *claimLabelRunner) createCalls() [][]string {
	var creates [][]string
	for _, call := range r.calls {
		if len(call) >= 3 && call[1] == "label" && call[2] == "create" {
			creates = append(creates, call)
		}
	}
	return creates
}

// stubOrchestratorClaimLabel stands in for the GitHub boundary in every test
// that reaches orchestrator provisioning without being about the claim label.
// The production runner shells out to `gh` and, when the label is absent,
// creates it — so a test that reached it would mint a real label on a real
// repository. Nothing in this package may cross that boundary.
func stubOrchestratorClaimLabel(context.Context, string, string, ...string) (string, error) {
	return "", nil
}

// claimLabelApp confines $HOME to a temp dir and stages the GitHub boundary, so
// ensuring the workspace writes nothing outside the test and mints no label
// anywhere real.
func claimLabelApp(t *testing.T, runner workingIssueCommandRunner) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	app := NewApp(erunUIDeps{
		store:                       newOrchestratorStubStore(t.TempDir()),
		runOrchestratorLabelCommand: runner,
	})
	app.investigations.reportDir = t.TempDir()
	return app
}

// TestOrchestratorProvisioningMintsTheClaimLabel is the reported failure: a new
// orchestrator id was provisioned with its role file and its SessionStart hook
// but no `wip:<id>` label, so `gh issue edit --add-label wip:<id>` — the first
// step of the claim protocol in erun-orchestrate — was rejected outright and
// the claim could not be made at all.
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
	// The repository is named on the command, not inferred from where it runs:
	// the probe runs in the shared orchestrators root, which is not a git
	// checkout, so `gh` there fails with "fatal: not a git repository".
	for _, callDir := range runner.dirs {
		if callDir != dir {
			t.Fatalf("label command ran in %q, want the orchestrators root %q", callDir, dir)
		}
	}
}

// TestOrchestratorProvisioningLeavesAnExistingClaimLabelAlone is the other
// direction: the label an id already has is what a live holder is claiming
// under, so provisioning must find it and stop rather than re-create it or
// rewrite its colour and description.
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

// TestOrchestratorClaimLabelProbeMatchesWholeNames pins the probe's comparison:
// an id is present only when the repository carries that exact label, so an
// orchestrator whose id is a prefix of another one's still gets its own.
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

// TestOrchestratorClaimLabelFailureIsReportedNotGuessedAt pins the failure
// contract. The step is best-effort — an unreachable GitHub must not stop an
// orchestrator launching — but a probe that could not answer must never be read
// as "the label is missing" and turned into a creation attempt against a
// repository erun could not read.
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

// TestOrchestratorClaimLabelSkippedWithoutAnID locks that a transient session —
// which has no id and claims nothing — reaches GitHub with nothing at all.
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

// TestOrchestratorClaimLabelRefusesAnIDThatCannotNameOne pins the trust
// boundary: a hand-edited config.yaml id is not trusted to name a label, since
// whitespace in one would make the probe's whole-line comparison meaningless.
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
