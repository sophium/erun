package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// repoRootForDockerignoreTest returns the repo root.
func repoRootForDockerignoreTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}

// TestRootDockerignoreExcludesDocsBuildArtifacts locks the fix for the
// erun-docs build failure: the erun-docs image runs `yarn install` +
// `yarn build` inside its Dockerfile, so node_modules/, build/, and
// .docusaurus/ are regenerated in-image and must never enter the build
// context.
//
// The real `docker build` only honours the context-root .dockerignore — it
// does NOT read erun-docs/.gitignore — so these directories must be excluded
// here. computeBuildFingerprint, by contrast, also honours nested .gitignore
// files (loadNestedGitignores), so it already drops them from the hash. That
// asymmetry is the bug: a fingerprint that excludes content the real context
// still ships diverges the two, bloats the context (~1GB of node_modules), and
// — depending on the daemon's state — can fail the build outright.
//
// This cannot be exercised from the integration subprocess harness: it has no
// real docker daemon, and the fingerprint excludes these dirs whether or not
// the root .dockerignore does, so only a direct check of the parsed root
// .dockerignore can catch the divergence.
func TestRootDockerignoreExcludesDocsBuildArtifacts(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	// An empty base anchors the root .dockerignore the same way real docker does.
	set := shared.ParseIgnoreData(data, "")

	excludedDirs := []string{
		"erun-docs/node_modules",
		"erun-docs/build",
		"erun-docs/.docusaurus",
	}
	for _, dir := range excludedDirs {
		if !set.Matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q from the build context (regenerated in-image by the erun-docs Dockerfile)", dir)
		}
		// docker walks the tree, so files beneath the directory must drop too.
		if !set.Matches(dir+"/index.js", false) {
			t.Errorf("root .dockerignore must exclude files under %q", dir)
		}
	}

	kept := []string{
		"erun-docs/package.json",
		"erun-docs/yarn.lock",
		"erun-docs/docusaurus.config.ts",
		"erun-docs/docs/intro.md",
	}
	for _, path := range kept {
		if set.Matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the erun-docs Dockerfile copies it)", path)
		}
	}
}

// TestRootDockerignoreMirrorsNestedGitignoresUnderCopiedModules is the same
// asymmetry as TestRootDockerignoreExcludesDocsBuildArtifacts above, found by
// diffing every nested .gitignore under a module the erun-devops test stage
// COPYs wholesale (erun-ui, erun-kit, erun-console) against this file: each of
// these local-only artifacts was excluded from computeBuildFingerprint via
// loadNestedGitignores but not from the real docker build context, so
// generating one on the host (a local `tsc`, `vite`, Playwright run, or just
// Finder) would ship it into the image without moving the fingerprint that
// decides whether the image rebuilds.
func TestRootDockerignoreMirrorsNestedGitignoresUnderCopiedModules(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	set := shared.ParseIgnoreData(data, "")

	excludedDirs := []string{
		"erun-ui/build/bin",
		"erun-ui/playwright/playwright/.cache",
		"erun-kit/.vite",
		"erun-console/.vite",
		"erun-console/playwright/.cache",
		"erun-devops/e2e/console/node_modules",
		"erun-devops/e2e/console/test-results",
		"erun-devops/e2e/console/playwright-report",
	}
	for _, dir := range excludedDirs {
		if !set.Matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q (mirrors a nested .gitignore entry)", dir)
		}
	}

	excludedFiles := []string{
		"erun-ui/frontend/tsconfig.tsbuildinfo",
		"erun-ui/frontend/package.json.md5",
		".DS_Store",
		"erun-kit/src/.DS_Store",
		"erun-kit/.env.local",
		"erun-kit/src/config/.env.local",
		"erun-console/.env.local",
		"erun-console/src/app/.env.local",
	}
	for _, path := range excludedFiles {
		if !set.Matches(path, false) {
			t.Errorf("root .dockerignore must exclude %q (mirrors a nested .gitignore entry)", path)
		}
	}

	kept := []string{
		"erun-ui/frontend/package.json",
		"erun-ui/playwright/tests/example.spec.ts",
		"erun-kit/src/index.ts",
		"erun-console/src/App.tsx",
		"erun-console/playwright/tests/example.spec.ts",
		"erun-devops/e2e/console/tests/console.spec.ts",
	}
	for _, path := range kept {
		if set.Matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the erun-devops Dockerfile copies it)", path)
		}
	}
}

// TestRootDockerignoreExcludesDesktopMacOSBuildOutputs: a local macOS desktop
// build leaves erun-ui/ERun.app and erun-ui/darwin beside the module, excluded
// from git only by the clone's own .git/info/exclude, which neither docker nor
// computeBuildFingerprint reads. Every Dockerfile that COPYs erun-ui (the
// erun-ui linux package's test stage among them) would ship ~90MB of host
// binaries in its context and move its fingerprint on every desktop build,
// rerunning an unchanged package's whole suite.
func TestRootDockerignoreExcludesDesktopMacOSBuildOutputs(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	set := shared.ParseIgnoreData(data, "")
	for _, dir := range []string{"erun-ui/ERun.app", "erun-ui/darwin"} {
		if !set.Matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q from the build context", dir)
		}
	}
	for _, path := range []string{"erun-ui/main.go", "erun-ui/darwin_cgo_flags.go", "erun-ui/app_activation_darwin.go"} {
		if set.Matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the erun-ui package builds it)", path)
		}
	}
	assertFingerprintSkipsDesktopMacOSBuildOutputs(t, data)
}

// The fingerprint reads the same file, so the excluded trees cannot move it.
func assertFingerprintSkipsDesktopMacOSBuildOutputs(t *testing.T, dockerignore []byte) {
	t.Helper()
	data := dockerignore
	context := t.TempDir()
	if err := os.WriteFile(filepath.Join(context, ".dockerignore"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	writeComponentTreeFile(t, context, "erun-ui/main.go", "package main\n")
	writeComponentTreeFile(t, context, "erun-ui/ERun.app/Contents/MacOS/erun-app", "binary")
	writeComponentTreeFile(t, context, "erun-ui/darwin/erun-app", "binary")
	ignored, err := LoadContextIgnoreSet(context, []string{"erun-ui"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := CollectFingerprintFiles(context, []string{"erun-ui"}, ignored)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "main.go" {
		t.Fatalf("the fingerprint must hash only erun-ui/main.go, got %v", files)
	}
}
