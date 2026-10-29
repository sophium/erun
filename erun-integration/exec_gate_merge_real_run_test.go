package integration

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// atlasBin fails rather than skips when atlas is absent: the suite must not gate on host capability.
func atlasBin(t testing.TB) string {
	t.Helper()
	path, err := osexec.LookPath("atlas")
	if err != nil {
		t.Fatalf(`the integration suite needs "atlas" on the host PATH: %v`, err)
	}
	return path
}

// runAtlasHash regenerates dir's migrations/atlas.sum with real atlas.
func runAtlasHash(t testing.TB, dir string) {
	t.Helper()
	cmd := harnessexec.Command(atlasBin(t), "migrate", "hash", "--dir", "file://migrations")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("atlas migrate hash: %v: %s", err, out)
	}
}

// captureAtlasValidate runs the real `atlas migrate validate` in dir.
func captureAtlasValidate(t testing.TB, dir string) (string, bool) {
	t.Helper()
	cmd := harnessexec.Command(atlasBin(t), "migrate", "validate", "--dir", "file://migrations")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func decodeGateMergeResult(t *testing.T, stdout string) model.GateMergeWorkingTreeResult {
	t.Helper()
	var parsed model.GateMergeWorkingTreeResult
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, stdout)
	}
	return parsed
}

func assertSingleSourceLandedOnMain(t *testing.T, parsed model.GateMergeWorkingTreeResult) {
	t.Helper()
	if parsed.TargetBranch != "main" || parsed.Remote != "origin" || parsed.Commit == "" || len(parsed.Skipped) != 0 {
		t.Fatalf("unexpected result: %+v", parsed)
	}
	if len(parsed.Landed) != 1 || parsed.Landed[0].SourceBranch != "feature" || parsed.Landed[0].SourceCommit == "" || parsed.Landed[0].Commit != parsed.Commit {
		t.Fatalf("unexpected landed sources: %+v", parsed.Landed)
	}
}

func assertNoOpSourceSkipped(t *testing.T, skipped []model.GateMergeSkippedSource) {
	t.Helper()
	if len(skipped) != 1 || skipped[0].SourceBranch != "already" {
		t.Fatalf("expected already to be skipped, got: %+v", skipped)
	}
	if !strings.Contains(skipped[0].Reason, "staged no changes") {
		t.Fatalf("expected the skip to name the no-op cause, got: %q", skipped[0].Reason)
	}
	if len(skipped[0].ConflictedFiles) != 0 {
		t.Fatalf("expected no conflicted files for a no-op source, got: %+v", skipped[0].ConflictedFiles)
	}
}

func assertConflictingMiddleSourceSkipped(t *testing.T, parsed model.GateMergeWorkingTreeResult) {
	t.Helper()
	if len(parsed.Landed) != 2 || parsed.Landed[0].SourceBranch != "a" || parsed.Landed[1].SourceBranch != "c" {
		t.Fatalf("expected a and c to land, got: %+v", parsed.Landed)
	}
	if len(parsed.Skipped) != 1 || parsed.Skipped[0].SourceBranch != "b" {
		t.Fatalf("expected b to be skipped, got: %+v", parsed.Skipped)
	}
	if len(parsed.Skipped[0].ConflictedFiles) != 1 || parsed.Skipped[0].ConflictedFiles[0] != "README.md" {
		t.Fatalf("expected README.md named as the conflicted file, got: %+v", parsed.Skipped[0])
	}
}

func testExecGateMergeRealRunSquashMergesOntoTarget(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	assertSingleSourceLandedOnMain(t, decodeGateMergeResult(t, result.Stdout))
	if branch := strings.TrimSpace(captureGit(t, setup.Cwd, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
		t.Fatalf("expected the worktree to land on main, got %q", branch)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt to be squash-merged onto main: %v", err)
	}
	parentCount := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%P"))
	if strings.Contains(parentCount, " ") {
		t.Fatalf("expected a single-parent squash commit, got parents: %q", parentCount)
	}
	message := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%s"))
	if message != "Add widget" {
		t.Fatalf("expected the squash commit message to be the given message, got %q", message)
	}
}

func testExecGateMergeRealRunCarriesTheBranchsClosesTrailer(t *testing.T) {
	// The blank line before Co-Authored-By: matters: the trailer block must not be read as only the final paragraph.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "Fix the widget\n\nCloses #2601\nReproduces: a caller reading the widget got its parts in the wrong order.\nRegression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Assemble the widget in declared order"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	if subject := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%s")); subject != "Assemble the widget in declared order" {
		t.Fatalf("the squash commit must still lead with the review name, got %q", subject)
	}
	grepped := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "main", "--grep=Closes #2601", "--format=%s"))
	if grepped != "Assemble the widget in declared order" {
		t.Fatalf("expected the landed squash commit to be findable by its issue reference, got %q", grepped)
	}
	body := captureGit(t, setup.Cwd, "log", "-1", "--pretty=%B")
	for _, want := range []string{
		"Reproduces: a caller reading the widget got its parts in the wrong order.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected the landed commit to carry %q, got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Fix the widget") {
		t.Fatalf("only the branch's trailers belong beneath the review name, not its subject, got:\n%s", body)
	}
}

func testExecGateMergeRealRunCarriesTrailersPastATrailingIssueReference(t *testing.T) {
	// The trailing bare "Refs #N" has no colon; the trailer walk must not stop at it.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "Fix the widget\n\nCloses #2662\nReproduces: a caller reading the widget got its parts in the wrong order.\nRegression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder\n\nRefs #2662")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Assemble the widget in declared order"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	if subject := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%s")); subject != "Assemble the widget in declared order" {
		t.Fatalf("the squash commit must still lead with the review name, got %q", subject)
	}
	grepped := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "main", "--grep=Closes #2662", "--format=%s"))
	if grepped != "Assemble the widget in declared order" {
		t.Fatalf("expected the landed squash commit to be findable by its issue reference, got %q", grepped)
	}
	body := captureGit(t, setup.Cwd, "log", "-1", "--pretty=%B")
	for _, want := range []string{
		"Reproduces: a caller reading the widget got its parts in the wrong order.",
		"Regression-Test: erun-common/widget_test.go::TestWidgetPartsKeepTheirDeclaredOrder",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected the landed commit to carry %q, got:\n%s", want, body)
		}
	}
	parsed := decodeGateMergeResult(t, result.Stdout)
	if len(parsed.Landed) != 1 {
		t.Fatalf("expected one landed source, got %+v", parsed.Landed)
	}
	carried := parsed.Landed[0].CarriedTrailers
	if len(carried) != 3 || carried[0] != "Closes #2662" {
		t.Fatalf("expected the result to name the three trailers it carried, got %q", carried)
	}
}

func testExecGateMergeRealRunCarriesAHardWrappedTrailerWhole(t *testing.T) {
	// The Reproduces: value is hard-wrapped at column 0; continuation lines must not end the trailer.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	const wrapped = "Reproduces: a hover card closed under a stationary pointer by the boot's own\n" +
		"default-landing open, read again as if it were still there -- expect's 10s\n" +
		"default expires with \"element(s) not found\" for the card that answered the\n" +
		"read before it."
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m",
		"Re-hover the orchestrator card\n\n"+wrapped+"\n"+
			"Regression-Test: erun-ui/playwright/tests/areas/orchestrator/orchestrator-restart-required.spec.ts::a hover card dropped while the boot lands is re-hovered, not read as absent\n")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Re-hover the orchestrator card"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}

	body := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "-1", "--pretty=%B"))
	if !strings.Contains(body, wrapped) {
		t.Fatalf("expected the landed commit to carry the branch's wrapped trailer whole, got:\n%s", body)
	}

	parsed := decodeGateMergeResult(t, result.Stdout)
	if len(parsed.Landed) != 1 {
		t.Fatalf("expected one landed source, got %+v", parsed.Landed)
	}
	carried := parsed.Landed[0].CarriedTrailers
	if len(carried) != 2 || carried[0] != wrapped {
		t.Fatalf("expected the run to report the wrapped trailer whole, got %q", carried)
	}
}

func testExecGateMergeRealRunAcceptsAUrlRemote(t *testing.T) {
	// A URL remote has no remote-tracking refs, so "<remote>/<branch>" must never be named.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	remoteRoot := seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	remoteURL := "file://" + remoteRoot
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main", "--remote", remoteURL, "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	parsed := decodeGateMergeResult(t, result.Stdout)
	if parsed.Remote != remoteURL {
		t.Fatalf("expected the URL remote to be reported back, got %q", parsed.Remote)
	}
	// An unresolvable source is skipped, not fatal, so assert it actually landed.
	if len(parsed.Landed) != 1 || parsed.Landed[0].SourceBranch != "feature" {
		t.Fatalf("expected feature to land against the URL remote, got landed=%+v skipped=%+v", parsed.Landed, parsed.Skipped)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt to be squash-merged onto main: %v", err)
	}
}

func testExecGateMergeRealRunBatchLandsMultipleSources(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "a")
	mustWriteFile(t, filepath.Join(setup.Cwd, "a.txt"), "a\n")
	fixture.RunGit(t, setup.Cwd, "add", "a.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "a commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "a")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "b")
	mustWriteFile(t, filepath.Join(setup.Cwd, "b.txt"), "b\n")
	fixture.RunGit(t, setup.Cwd, "add", "b.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "b commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "b")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "a", "--source", "b", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add a\x00Add b"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	parsed := decodeGateMergeResult(t, result.Stdout)
	if len(parsed.Landed) != 2 || len(parsed.Skipped) != 0 {
		t.Fatalf("expected both sources to land with nothing skipped: %+v", parsed)
	}
	if parsed.Landed[0].SourceBranch != "a" || parsed.Landed[1].SourceBranch != "b" {
		t.Fatalf("expected sources to land in the order given, got: %+v", parsed.Landed)
	}
	if parsed.Commit != parsed.Landed[1].Commit {
		t.Fatalf("expected the result's tip commit to be the last landed source's commit: %+v", parsed)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(setup.Cwd, name)); err != nil {
			t.Fatalf("expected %s to be squash-merged onto main: %v", name, err)
		}
	}
	subjects := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "--format=%s", "main~2..main"))
	if subjects != "Add b\nAdd a" {
		t.Fatalf("expected two stacked squash commits, one per source, got: %q", subjects)
	}
}

func testExecGateMergeRealRunSkipsASourceThatContributesNothing(t *testing.T) {
	// A source that stages nothing is skipped, not a batch failure.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "already")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "already")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "feature.txt"), "feature\n")
	fixture.RunGit(t, setup.Cwd, "add", "feature.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "already", "--source", "feature", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Already\x00Add feature"})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 — a source that contributes nothing must not fail the batch:\n%s", result.Combined)
	}
	parsed := decodeGateMergeResult(t, result.Stdout)
	if len(parsed.Landed) != 1 || parsed.Landed[0].SourceBranch != "feature" {
		t.Fatalf("expected feature to land, got: %+v", parsed.Landed)
	}
	assertNoOpSourceSkipped(t, parsed.Skipped)
	if !strings.Contains(result.Combined, "Skipped") {
		t.Fatalf("expected the CLI to report the skip, got:\n%s", result.Combined)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt to be squash-merged onto main: %v", err)
	}
	subjects := strings.TrimSpace(captureGit(t, setup.Cwd, "log", "--format=%s", "main~1..main"))
	if subjects != "Add feature" {
		t.Fatalf("expected exactly one squash commit, for the contributing source only, got: %q", subjects)
	}
}

func testExecGateMergeRealRunBatchSkipsAConflictingSourceAndLandsTheRest(t *testing.T) {
	// The conflicting middle source is aborted and skipped; the third still lands on the clean tree.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "a")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "a change\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "a edits readme")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "a")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "b")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "conflicting b change\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "b edits readme too")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "b")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "c")
	mustWriteFile(t, filepath.Join(setup.Cwd, "c.txt"), "c\n")
	fixture.RunGit(t, setup.Cwd, "add", "c.txt")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "c commit")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "c")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "a", "--source", "b", "--source", "c", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add a\x00Add b\x00Add c"})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0 — the batch still lands two of three sources:\n%s", result.Combined)
	}
	assertConflictingMiddleSourceSkipped(t, decodeGateMergeResult(t, result.Stdout))
	if !strings.Contains(result.Combined, "Skipped") {
		t.Fatalf("expected the CLI to report the skip, got:\n%s", result.Combined)
	}
	status := strings.TrimSpace(captureGit(t, setup.Cwd, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected the working tree to be left clean after the aborted conflict, got status: %q", status)
	}
	for _, name := range []string{"README.md", "c.txt"} {
		if _, err := os.Stat(filepath.Join(setup.Cwd, name)); err != nil {
			t.Fatalf("expected %s to exist on the landed stack: %v", name, err)
		}
	}
}

func testExecGateMergeRealRunAllSourcesConflictRefuses(t *testing.T) {
	// A batch that lands nothing refuses, and the conflict is aborted rather than left in the tree.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "feature")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "feature change\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "feature edits readme")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "feature")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "main change\n")
	fixture.RunGit(t, setup.Cwd, "add", "README.md")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "main edits readme")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature", "--target", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when every source is skipped, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "conflicted") || !strings.Contains(result.Combined, "no source branch landed") {
		t.Fatalf("expected the conflict and the no-source-landed refusal, got:\n%s", result.Combined)
	}
	status := strings.TrimSpace(captureGit(t, setup.Cwd, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected the worktree to be left clean (aborted, not mid-conflict), got status: %q", status)
	}
}

func testExecGateMergeRealRunBatchLandsMigrationsWithConflictingAtlasSum(t *testing.T) {
	// atlas.sum conflicts by construction when two branches add migrations; it must resolve automatically
	// and still validate and catch later tampering.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260101000000_init.sql"), "CREATE TABLE foo (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "seed initial migration")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "a")
	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260102000000_add_bar.sql"), "CREATE TABLE bar (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add bar")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "a")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "b")
	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260103000000_add_baz.sql"), "CREATE TABLE baz (id int);\n")
	runAtlasHash(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", "migrations")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add baz")
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "b")

	fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")

	envVars := append(setup.Env(), "ERUN_ATLAS_BIN="+atlasBin(t))
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "a", "--source", "b", "--target", "main", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add bar\x00Add baz"})
	if result.ExitCode != 0 {
		t.Fatalf("expected the batch to land with nothing for a human to resolve, got exit %d:\n%s", result.ExitCode, result.Combined)
	}
	parsed := decodeGateMergeResult(t, result.Stdout)
	if len(parsed.Landed) != 2 || len(parsed.Skipped) != 0 {
		t.Fatalf("expected both migrations to land with nothing skipped, got: %+v", parsed)
	}
	for _, name := range []string{"20260102000000_add_bar.sql", "20260103000000_add_baz.sql"} {
		if _, err := os.Stat(filepath.Join(setup.Cwd, "migrations", name)); err != nil {
			t.Fatalf("expected %s to be squash-merged onto main: %v", name, err)
		}
	}
	status := strings.TrimSpace(captureGit(t, setup.Cwd, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean worktree after landing, got status: %q", status)
	}

	if out, ok := captureAtlasValidate(t, setup.Cwd); !ok {
		t.Fatalf("expected the regenerated atlas.sum to validate cleanly, got:\n%s", out)
	}

	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260102000000_add_bar.sql"), "CREATE TABLE bar (id int, tampered boolean);\n")
	out, ok := captureAtlasValidate(t, setup.Cwd)
	if ok {
		t.Fatalf("expected atlas to catch a migration edited after the fact, but validate passed:\n%s", out)
	}
	if !strings.Contains(out, "checksum") {
		t.Fatalf("expected a checksum-mismatch report, got:\n%s", out)
	}
}
