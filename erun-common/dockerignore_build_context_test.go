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

// TestRootDockerignoreExcludesTerraformInitArtifacts is the same asymmetry as
// TestRootDockerignoreExcludesDocsBuildArtifacts above, for the terraform
// module tree the erun-devops test stage COPYs so `make check`'s terraform-test
// target can run the published modules' `terraform test` suites.
//
// `terraform init` writes `.terraform/` (provider binaries, tens of MB) and
// `.terraform.lock.hcl` beside the module it initialises, and the new target
// runs init in-tree -- so a developer who ran it on the host has both. The
// root .gitignore already drops them, which means computeBuildFingerprint drops
// them too, while the real `docker build` honours only this root .dockerignore.
// Without the exclusions below that is the erun-docs shape again: the context
// ships what the fingerprint says is not there.
func TestRootDockerignoreExcludesTerraformInitArtifacts(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	data, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatalf("read root .dockerignore: %v", err)
	}
	set := parseIgnoreData(data, "")

	const module = "erun-devops/terraform-erun/modules/terraform-erun-cluster-edge"
	excluded := []struct {
		path  string
		isDir bool
	}{
		{module + "/.terraform", true},
		// A file beneath the init dir: docker walks the tree, so excluding the
		// directory has to drop its contents too.
		{module + "/.terraform/providers/registry.terraform.io/hashicorp/helm/2.17.0/linux_amd64/terraform-provider-helm_v2.17.0", false},
		{module + "/.terraform.lock.hcl", false},
	}
	for _, e := range excluded {
		if !set.matches(e.path, e.isDir) {
			t.Errorf("root .dockerignore must exclude %q (written by a local `terraform init`; the test stage COPYs erun-devops/terraform-erun)", e.path)
		}
	}

	// The modules themselves are the target's input, so the exclusion must not
	// swallow them -- a pattern loose enough to drop `.terraform` must still
	// keep the sources and the suites beside it.
	kept := []string{
		module + "/main.tf",
		module + "/versions.tf",
		module + "/tests/edge_transport_policy.tftest.hcl",
		"erun-devops/terraform-erun/modules/terraform-erun-cloudflare-apex/tests/apex_records.tftest.hcl",
	}
	for _, path := range kept {
		if set.matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the terraform-test target reads it)", path)
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
	set := parseIgnoreData(data, "")

	excludedDirs := []string{
		"erun-ui/build/bin",
		"erun-ui/playwright/playwright/.cache",
		"erun-kit/.vite",
		"erun-console/.vite",
		"erun-console/playwright/.cache",
	}
	for _, dir := range excludedDirs {
		if !set.matches(dir, true) {
			t.Errorf("root .dockerignore must exclude %q (mirrors a nested .gitignore entry)", dir)
		}
	}

	excludedFiles := []string{
		"erun-ui/frontend/tsconfig.tsbuildinfo",
		"erun-ui/frontend/package.json.md5",
		"erun-console/playwright/.e2e-oidc.env",
		".DS_Store",
		"erun-kit/src/.DS_Store",
		"erun-kit/.env.local",
		"erun-kit/src/config/.env.local",
		"erun-console/.env.local",
		"erun-console/src/app/.env.local",
	}
	for _, path := range excludedFiles {
		if !set.matches(path, false) {
			t.Errorf("root .dockerignore must exclude %q (mirrors a nested .gitignore entry)", path)
		}
	}

	kept := []string{
		"erun-ui/frontend/package.json",
		"erun-ui/playwright/tests/example.spec.ts",
		"erun-kit/src/index.ts",
		"erun-console/src/App.tsx",
		"erun-console/playwright/tests/example.spec.ts",
	}
	for _, path := range kept {
		if set.matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the erun-devops Dockerfile copies it)", path)
		}
	}
}
