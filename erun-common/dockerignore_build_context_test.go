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
// module tree the erun-devops test stage COPYs so `make check`'s
// terraform-module-tests target can run the published modules' `terraform test`
// suites.
//
// `terraform init` writes `.terraform/` (provider binaries, tens of MB) beside
// the module it initialises, and the new target runs init in-tree -- so a
// developer who ran it on the host has one. The root .gitignore already drops
// it, which means computeBuildFingerprint drops it too, while the real `docker
// build` honours only this root .dockerignore. Without the exclusion below that
// is the erun-docs shape again: the context ships what the fingerprint says is
// not there.
//
// The file-beneath-the-directory case is the one that pins the pattern's FORM,
// and it is the assertion that fails on the obvious-looking alternative. A
// trailing slash makes the pattern directory-only in erun's own matcher, which
// then skips it for every file (build_incremental.go: `if p.dirOnly && !isDir {
// continue }`), so `**/.terraform/` matches the directory and nothing under it.
//
// The two surfaces read that slash differently, which is what makes it silent
// rather than loud: the matcher above drops every file, while docker's own
// .dockerignore treats a trailing slash as insignificant and still excludes the
// whole subtree from the real context. So the divergence is one-directional --
// the real build excludes `.terraform`, and computeBuildFingerprint, which is
// what loadContextIgnoreSet feeds, does not. Writing the pattern without the
// slash is what makes both agree, and this assertion is what fails when it
// grows one back.
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
		// directory has to drop its contents too. See the comment above -- this
		// is the case a trailing-slash pattern silently fails.
		{module + "/.terraform/providers/registry.terraform.io/hashicorp/helm/2.17.0/linux_amd64/terraform-provider-helm_v2.17.0", false},
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
		// The lock file is committed source and must stay IN the context: the
		// image bakes its provider mirror from exactly these pins at build time
		// (scripts/terraform-providers-mirror.sh), and the gate initializes with
		// -lockfile=readonly against what the bake mirrored. Excluding it here
		// would leave the bake with nothing to read and push the gate back to
		// resolving newest-satisfying from the registry, which is the network
		// dependency this whole design removes.
		module + "/.terraform.lock.hcl",
	}
	for _, path := range kept {
		if set.matches(path, false) {
			t.Errorf("root .dockerignore must NOT exclude %q (the terraform-module-tests target reads it)", path)
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
