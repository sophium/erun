package fingerprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/build/ignore"
)

func repoRootForDockerignoreTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}

// docker build reads only the root .dockerignore, while the fingerprint also honours nested
// .gitignore files; erun-docs' in-image yarn outputs must be excluded here too.
func TestRootDockerignoreExcludesDocsBuildArtifacts(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	// An empty base anchors the root .dockerignore the same way real docker does.
	set := ignore.ParseIgnoreData(data, "")

	excludedDirs := []string{
		"erun-docs/node_modules",
		"erun-docs/build",
		"erun-docs/.docusaurus",
	}
	for _, dir := range excludedDirs {
		if !set.Matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q from the build context (regenerated in-image by the erun-docs Dockerfile)", dir)
		}
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

// Each nested .gitignore entry under a wholesale-COPYed module must be mirrored here.
func TestRootDockerignoreMirrorsNestedGitignoresUnderCopiedModules(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	set := ignore.ParseIgnoreData(data, "")

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

// Desktop build outputs are excluded only by .git/info/exclude, which neither docker nor the fingerprint reads.
func TestRootDockerignoreExcludesDesktopMacOSBuildOutputs(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	set := ignore.ParseIgnoreData(data, "")
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
	ignored, err := ignore.LoadContextIgnoreSet(context, []string{"erun-ui"})
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
