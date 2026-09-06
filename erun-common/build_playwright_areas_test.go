package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFileForTest(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestDockerfileConsumesPlaywrightTestAreas(t *testing.T) {
	dir := t.TempDir()
	withArg := filepath.Join(dir, "Dockerfile.with")
	if err := os.WriteFile(withArg, []byte("FROM golang\nARG PLAYWRIGHT_TEST_AREAS=\"\"\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	if !dockerfileConsumesPlaywrightTestAreas(withArg) {
		t.Fatalf("expected Dockerfile declaring the ARG to be detected")
	}

	withoutArg := filepath.Join(dir, "Dockerfile.without")
	if err := os.WriteFile(withoutArg, []byte("FROM golang\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	if dockerfileConsumesPlaywrightTestAreas(withoutArg) {
		t.Fatalf("expected Dockerfile without the ARG to be undetected")
	}

	if dockerfileConsumesPlaywrightTestAreas(filepath.Join(dir, "missing")) {
		t.Fatalf("expected a missing Dockerfile to be undetected, not to panic or false-positive")
	}
}

// newPlaywrightAreaTestRepo lays out just enough of the real tree shape
// (erun-ui/playwright/tests/{smoke,areas/<area>,fixtures,pages}) on branch
// "main" for resolvePlaywrightTestAreaSelection to classify a later diff
// against it, then checks out a feature branch so HEAD moves independently
// of main -- the same shape newAgentJobTestRepo (job_worktree_test.go) uses
// for other git-plumbing tests in this package.
func newPlaywrightAreaTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, dir, "init", "-q", "-b", "main")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	writeFileForTest(t, dir, "erun-ui/playwright/tests/smoke/smoke.spec.ts", "// smoke\n")
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/sidebar/sidebar.spec.ts", "// sidebar\n")
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/manage/manage.spec.ts", "// manage\n")
	writeFileForTest(t, dir, "erun-ui/playwright/fixtures/erunApp.ts", "// fixtures\n")
	writeFileForTest(t, dir, "erun-ui/frontend/src/App.tsx", "// app\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "seed")
	runGitForTest(t, dir, "checkout", "-q", "-b", "feature")
	return dir
}

func TestResolvePlaywrightTestAreaSelectionNoSpecChangeIsSmokeOnly(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/frontend/src/App.tsx", "// app changed\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "source only")

	selection, ok := resolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatalf("expected selection to resolve")
	}
	if selection != "smoke" {
		t.Fatalf("expected smoke-only selection for a source-only diff, got %q", selection)
	}
}

func TestResolvePlaywrightTestAreaSelectionOneAreaChanged(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/sidebar/sidebar-new.spec.ts", "// new sidebar spec\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "sidebar spec")

	selection, ok := resolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatalf("expected selection to resolve")
	}
	if selection != "smoke,sidebar" {
		t.Fatalf("expected smoke+sidebar selection, got %q", selection)
	}
}

func TestResolvePlaywrightTestAreaSelectionTwoAreasChanged(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/sidebar/sidebar-new.spec.ts", "// new sidebar spec\n")
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/manage/manage-new.spec.ts", "// new manage spec\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "two areas")

	selection, ok := resolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatalf("expected selection to resolve")
	}
	if selection != "smoke,manage,sidebar" {
		t.Fatalf("expected sorted smoke+manage+sidebar selection, got %q", selection)
	}
}

func TestResolvePlaywrightTestAreaSelectionFixturesChangeRunsEverything(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/playwright/fixtures/erunApp.ts", "// fixtures changed\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "shared fixture change")

	selection, ok := resolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatalf("expected selection to resolve")
	}
	if selection != "all" {
		t.Fatalf("expected \"all\" for a shared-fixture change, got %q", selection)
	}
}

func TestResolvePlaywrightTestAreaSelectionUncommittedAndUntrackedFilesCount(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	// Uncommitted (unstaged) change to an existing tracked file.
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/manage/manage.spec.ts", "// manage changed, uncommitted\n")
	// Untracked new spec file, never `git add`ed.
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/sidebar/sidebar-untracked.spec.ts", "// untracked\n")

	selection, ok := resolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatalf("expected selection to resolve")
	}
	if selection != "smoke,manage,sidebar" {
		t.Fatalf("expected uncommitted and untracked spec changes to both count, got %q", selection)
	}
}

func TestResolvePlaywrightTestAreaSelectionNoMergeBaseFailsSafe(t *testing.T) {
	dir := t.TempDir()
	runGitForTest(t, dir, "init", "-q", "-b", "solo")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	writeFileForTest(t, dir, "seed.txt", "seed\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "seed")

	if _, ok := resolvePlaywrightTestAreaSelection(testContext(), dir); ok {
		t.Fatalf("expected no resolvable merge base against any candidate branch to fail safe (ok=false)")
	}
}

func TestApplyPlaywrightAreaBuildArgsSkipsWhenDockerfileDoesNotDeclareTheArg(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM golang\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	build := &DockerBuildSpec{DockerfilePath: dockerfile}
	applyPlaywrightAreaBuildArgs(testContext(), dir, build)
	if build.PlaywrightTestAreas != "" {
		t.Fatalf("expected PlaywrightTestAreas to stay empty when the Dockerfile declares no matching ARG, got %q", build.PlaywrightTestAreas)
	}
}

func TestApplyPlaywrightAreaBuildArgsSetsSelectionWhenDockerfileDeclaresTheArg(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/sidebar/sidebar-new.spec.ts", "// new sidebar spec\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "sidebar spec")

	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM golang\nARG PLAYWRIGHT_TEST_AREAS=\"\"\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	build := &DockerBuildSpec{DockerfilePath: dockerfile}
	applyPlaywrightAreaBuildArgs(testContext(), dir, build)
	if build.PlaywrightTestAreas != "smoke,sidebar" {
		t.Fatalf("expected PlaywrightTestAreas=smoke,sidebar, got %q", build.PlaywrightTestAreas)
	}
}
