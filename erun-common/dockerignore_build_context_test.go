package eruncommon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRootForDockerignoreTest returns the repo root. erun-common sits directly
// under the root, so the grandparent of this file is the root regardless of the
// test's working directory.
func repoRootForDockerignoreTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
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
	set := parseIgnoreData(data, "")

	excludedDirs := []string{
		"erun-docs/node_modules",
		"erun-docs/build",
		"erun-docs/.docusaurus",
	}
	for _, dir := range excludedDirs {
		if !set.matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q from the build context (regenerated in-image by the erun-docs Dockerfile)", dir)
		}
		// docker walks the tree, so files beneath the directory must drop too.
		if !set.matches(dir+"/index.js", false) {
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
		if set.matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the erun-docs Dockerfile copies it)", path)
		}
	}
}
