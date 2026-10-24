package integration

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/mergequeue"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// atlasBin resolves the real atlas binary from the host PATH so a scenario
// can seed and validate a real Atlas migration checksum end to end: the
// erun-devops Dockerfile's test stage installs atlas for exactly this reason,
// so — per this suite's "nothing may gate on host capability" rule —
// this fails loudly rather than skipping when it is unexpectedly absent.
func atlasBin(t testing.TB) string {
	t.Helper()
	path, err := osexec.LookPath("atlas")
	if err != nil {
		t.Fatalf(`the integration suite needs "atlas" on the host PATH: %v`, err)
	}
	return path
}

// runAtlasHash regenerates dir's migrations/atlas.sum for real, the same way
// a migration's author would before committing, so a fixture branch carries a
// real Atlas checksum rather than a hand-authored placeholder.
func runAtlasHash(t testing.TB, dir string) {
	t.Helper()
	cmd := harnessexec.Command(atlasBin(t), "migrate", "hash", "--dir", "file://migrations")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("atlas migrate hash: %v: %s", err, out)
	}
}

// captureAtlasValidate runs the real `atlas migrate validate` against dir's
// migrations directory, so a scenario can assert atlas's own tamper-detection
// contract end to end rather than trusting that regeneration preserved it.
func captureAtlasValidate(t testing.TB, dir string) (string, bool) {
	t.Helper()
	cmd := harnessexec.Command(atlasBin(t), "migrate", "validate", "--dir", "file://migrations")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func decodeGateMergeResult(t *testing.T, stdout string) mergequeue.GateMergeWorkingTreeResult {
	t.Helper()
	var parsed mergequeue.GateMergeWorkingTreeResult
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("decode --output json: %v\n%s", err, stdout)
	}
	return parsed
}

// assertSingleSourceLandedOnMain checks a gate-merge of the lone branch
// "feature" onto main through origin.
func assertSingleSourceLandedOnMain(t *testing.T, parsed mergequeue.GateMergeWorkingTreeResult) {
	t.Helper()
	if parsed.TargetBranch != "main" || parsed.Remote != "origin" || parsed.Commit == "" || len(parsed.Skipped) != 0 {
		t.Fatalf("unexpected result: %+v", parsed)
	}
	if len(parsed.Landed) != 1 || parsed.Landed[0].SourceBranch != "feature" || parsed.Landed[0].SourceCommit == "" || parsed.Landed[0].Commit != parsed.Commit {
		t.Fatalf("unexpected landed sources: %+v", parsed.Landed)
	}
}

// assertNoOpSourceSkipped checks that the lone skipped source is "already",
// reported as staging nothing rather than as a conflict.
func assertNoOpSourceSkipped(t *testing.T, skipped []mergequeue.GateMergeSkippedSource) {
	t.Helper()
	if len(skipped) != 1 || skipped[0].SourceBranch != "already" {
		t.Fatalf("expected already to be skipped, got: %+v", skipped)
	}
	// The skip must say it staged nothing — not be misreported as a
	// conflict, the other thing that produces a skip.
	if !strings.Contains(skipped[0].Reason, "staged no changes") {
		t.Fatalf("expected the skip to name the no-op cause, got: %q", skipped[0].Reason)
	}
	if len(skipped[0].ConflictedFiles) != 0 {
		t.Fatalf("expected no conflicted files for a no-op source, got: %+v", skipped[0].ConflictedFiles)
	}
}

// assertConflictingMiddleSourceSkipped checks the a/b/c batch where b
// conflicts on README.md with what a already landed.
func assertConflictingMiddleSourceSkipped(t *testing.T, parsed mergequeue.GateMergeWorkingTreeResult) {
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
	// A real bare remote and a real divergent source branch, so "the squash
	// merge actually landed on target" is an observable fact rather than a
	// stubbed git call.
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
	// The reported failure, on a real repository: the branch's own commit
	// declares "Closes #N", the merge queue lands it with the review name
	// as the squash message, and the declaration is gone — the issue stays
	// open with its fix on the target and no commit there referencing it.
	// The caller here passes only the review name, which is the state the
	// report describes; a caller that passes the branch's message verbatim
	// already carried the trailer by accident.
	//
	// The trailers are separated from a trailing "Co-Authored-By:" by a
	// blank line, because that is the shape the report was observed on: the
	// carriage read only the final run of non-empty lines, so that run was
	// the Co-Authored-By: alone and every declaration above it was
	// discarded. Without that blank line the scenario never reached the
	// failure it exists for.
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
	// The observable behaviour the report is about: a reader grepping the
	// target for the issue reference finds the commit that closed it.
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
	// The reported failure, on a real repository: the branch's commit
	// declares "Closes #N" and the reproduction trailers a defect fix
	// declares, and closes with a bare "Refs #N" — the same "Token #N"
	// spelling "Closes #N" uses, so it carries no colon to read as a
	// trailer token. The block walk stopped at that paragraph, read an
	// empty block, and squash-merged the branch under the review name
	// alone: every declaration was gone, the issue stayed open with its fix
	// on the target, and nothing said so. The "Refs #N" is the whole point
	// of the scenario — without it the branch lands its trailers and the
	// failure this exists for is never reached.
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
	// The observable behaviour the report is about: a reader grepping the
	// target for the issue reference finds the commit that closed it.
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
	// The other half of the guarantee: the run says what it carried, so a
	// carriage that found nothing is never left to be counted out of the
	// squash message afterwards.
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
	// The reported failure, on a real repository: the branch's own commit
	// declares a "Reproduces:" whose value is hard-wrapped across four
	// physical lines, each continuation starting at column 0 — how a long
	// hand-written value is wrapped, and how the value on the landing this was
	// observed on was written. The carriage read a trailer entry as ending at the
	// first line that looked like a "Token value", which nearly every
	// continuation line does, so the branch landed with only the first
	// physical line: the commit on the target asserted a reproduction that
	// ended mid-clause, and the truncation was invisible because the shortened
	// line still matched the trailer pattern.
	//
	// The wrap is the whole point of the scenario — without it the branch
	// lands its trailer intact and the failure this exists for is never
	// reached. The assertions read the tail of the value, which is the part
	// the truncation dropped.
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

	// The observable behaviour the report is about: the landed commit's
	// "Reproduces:" is the value its author wrote, whole.
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
	// --remote takes a URL, not only a configured remote name. A URL creates
	// no remote-tracking refs, so "<remote>/<branch>" does not exist for one
	// and must not be named by the checkout or any squash-merge. A file://
	// URL carries the ":" and "//" that make it unnameable as a ref without
	// reaching the network.
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
	// A source that could not be resolved is skipped, not fatal, so a
	// regression here would otherwise read as a clean empty run.
	if len(parsed.Landed) != 1 || parsed.Landed[0].SourceBranch != "feature" {
		t.Fatalf("expected feature to land against the URL remote, got landed=%+v skipped=%+v", parsed.Landed, parsed.Skipped)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt to be squash-merged onto main: %v", err)
	}
}

func testExecGateMergeRealRunBatchLandsMultipleSources(t *testing.T) {
	// Two independent branches, both squashed onto one working tree by one
	// gate-merge call — the batching this generalization exists for: a
	// caller no longer has to reset from the target and repeat the call per
	// branch, which would discard every landed branch but the last.
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
	// The no-op half of the same report. A source whose content is already
	// on the target — here a branch still at main's own tip, so `git merge
	// --squash` reports "Already up to date" and stages nothing — used to
	// abort the whole batch: the code asked git to commit the empty squash
	// anyway, that commit exited non-zero, and the failure came back as a
	// bare exit status that named no cause (git explains it on stdout,
	// which the commit call discarded). A no-op source contributes nothing
	// and must be skipped like a conflicting one, with the rest of the
	// batch still gating — an already-landed branch is a no-op, not a dead
	// gate.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedBareOrigin(t, setup)

	// `already` never diverges from main, so squashing it stages nothing.
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
	// The conflict-skip path this generalization exists for: a batch of
	// three, where the middle source conflicts with what the first source
	// already landed. It is skipped (merge --abort, recorded with a
	// reason) rather than failing the whole call, and the third source
	// still lands against the clean tree the abort left behind.
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
	// A single conflicting source (the ordinary one-branch gate) is
	// skipped like any other, but a batch that lands nothing has nothing
	// to gate, so the command refuses rather than reporting success on an
	// unchanged target. The worktree is left clean — the conflict was
	// backed out with `git merge --abort`, not left mid-conflict.
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
	// migrations/atlas.sum is a generated checksum over every migration file
	// in the directory, so two branches that each add a migration conflict on
	// it by construction; two such branches must batch-gate together with
	// nothing for a human to resolve. It also proves the resolution doesn't
	// weaken what
	// atlas.sum is for: after the batch lands, the real `atlas migrate
	// validate` still passes, and a migration edited after the fact is
	// still caught.
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

	// The regenerated atlas.sum is not a plausible-looking placeholder: it
	// passes atlas's own real integrity check against the migration files
	// that actually landed.
	if out, ok := captureAtlasValidate(t, setup.Cwd); !ok {
		t.Fatalf("expected the regenerated atlas.sum to validate cleanly, got:\n%s", out)
	}

	// And the check still does its job: editing a migration after the
	// fact, with no matching atlas.sum update, is still caught — this fix
	// teaches the tooling to regenerate on conflict, not to stop checking.
	mustWriteFile(t, filepath.Join(setup.Cwd, "migrations", "20260102000000_add_bar.sql"), "CREATE TABLE bar (id int, tampered boolean);\n")
	out, ok := captureAtlasValidate(t, setup.Cwd)
	if ok {
		t.Fatalf("expected atlas to catch a migration edited after the fact, but validate passed:\n%s", out)
	}
	if !strings.Contains(out, "checksum") {
		t.Fatalf("expected a checksum-mismatch report, got:\n%s", out)
	}
}
