package integration

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// validScoopManifest satisfies every invariant the release-time Scoop
// validation enforces: a mingw dependency, the MinGW/Wails CGO prerequisite
// wording (and no stale Fyne wording), a non-empty installer script, and all
// four shipped executables in bin.
const validScoopManifest = `{
  "version": "1.0.0",
  "description": "erun developer toolkit",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": ["go", "mingw", "nodejs", "yarn"],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.0.0.zip",
  "hash": "0000000000000000000000000000000000000000000000000000000000000000",
  "extract_dir": "erun-1.0.0",
  "installer": {
    "script": [
      "if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw 'building erun-app.exe requires a C compiler such as MinGW for the Wails CGO build' }",
      "go build -trimpath -o \"$dir\\erun.exe\" ."
    ]
  },
  "bin": ["erun.exe", "emcp.exe", "eapi.exe", "erun-app.exe"]
}
`

// scoopManifestMissingMingwAndBin violates four invariants at once: no mingw
// dependency, stale Fyne wording instead of the MinGW/Wails prerequisite, and a
// missing erun-app.exe in bin.
const scoopManifestMissingMingwAndBin = `{
  "version": "1.0.0",
  "description": "erun developer toolkit",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": ["go", "nodejs", "yarn"],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.0.0.zip",
  "hash": "0000000000000000000000000000000000000000000000000000000000000000",
  "extract_dir": "erun-1.0.0",
  "installer": {
    "script": [
      "if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw 'building erun-app.exe requires the Fyne toolchain prerequisites' }"
    ]
  },
  "bin": ["erun.exe", "emcp.exe", "eapi.exe"]
}
`

// scoopManifestEmptyScript keeps depends and bin valid but empties the
// installer script, tripping both the non-empty-script and MinGW-wording
// invariants.
const scoopManifestEmptyScript = `{
  "version": "1.0.0",
  "description": "erun developer toolkit",
  "homepage": "https://github.com/sophium/erun",
  "license": "MIT",
  "depends": ["go", "mingw", "nodejs", "yarn"],
  "url": "https://github.com/sophium/erun/archive/refs/tags/v1.0.0.zip",
  "hash": "0000000000000000000000000000000000000000000000000000000000000000",
  "extract_dir": "erun-1.0.0",
  "installer": {
    "script": []
  },
  "bin": ["erun.exe", "emcp.exe", "eapi.exe", "erun-app.exe"]
}
`

func TestRelease(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"release", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_in_git_repo", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "release/dry_run_in_git_repo", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_develop_emits_candidate_plan", func(t *testing.T) {
		// Exercises release.go on the develop branch: --dry-run must emit a
		// `mode=candidate` plan with sync-remote / push stages, the
		// resolved docker image reference, and the rc.<count> version
		// suffix on stdout.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_develop_emits_candidate_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_develop_emits_stable_plan", func(t *testing.T) {
		// Exercises release.go stable path on main when develop also
		// exists: --dry-run must include both sync-remote (main) and
		// sync-develop stages, and push to both branches.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "branch", "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_develop_emits_stable_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_without_develop_pushes_only_main", func(t *testing.T) {
		// Exercises release.go stable path on main when develop does not
		// exist: --dry-run must not include sync-develop or develop in
		// the push target.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_without_develop_pushes_only_main", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_includes_linux_release_scripts", func(t *testing.T) {
		// Exercises release.go linux package release path: --dry-run must
		// trace the per-component release script invocation with
		// ERUN_BUILD_VERSION when the host supports Linux package builds.
		// LinuxPackageBuildsSupported requires GOOS=linux and dpkg-deb in
		// PATH, so skip on hosts that cannot reach the support branch
		// (notably macOS dev machines). A second component carrying only a
		// build.sh (no release.sh) drives discoverReleaseLinuxScripts'
		// skip-component branch: it is a valid linux package context but
		// contributes no release script, so the trace shows only erun-cli's.
		if runtime.GOOS != "linux" {
			t.Skip("linux release scripts only run on Linux hosts")
		}
		if _, err := osexec.LookPath("dpkg-deb"); err != nil {
			t.Skip("linux release scripts require dpkg-deb in PATH")
		}
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		linuxComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli")
		if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write release.sh: %v", err)
		}
		buildOnlyComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-app")
		if err := os.MkdirAll(buildOnlyComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir build-only linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(buildOnlyComponentDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux release script")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_includes_linux_release_scripts", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_darwin_host_skips_linux_release_scripts", func(t *testing.T) {
		// A project shipping linux package release scripts, resolved on a
		// host that cannot build them: discoverSupportedReleaseLinuxScripts
		// must drop the scripts and runReleaseSpec must trace the
		// "skipping linux package scripts" decision instead of silently
		// omitting them. ERUN_HOST_OS_OVERRIDE=darwin pins the unsupported
		// branch so the golden is deterministic on every host, including
		// the Linux CI machines where the support check would pass.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		linuxComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli")
		if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write release.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux release script")

		envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_darwin_host_skips_linux_release_scripts", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_marketplace_emits_sha_sync", func(t *testing.T) {
		// Exercises release.go marketplace.json bump path: when the project
		// contains a .claude-plugin/marketplace.json, the sync-packaging-checksums
		// stage must trace `git rev-parse v<VERSION>^{}` (to resolve the release
		// commit) and include the marketplace.json path in the git-add list.
		// The bump itself is gated on !DryRun so the trace alone proves the path
		// is wired correctly.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedMarketplaceJSON(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "add", ".claude-plugin")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add marketplace.json")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_marketplace_emits_sha_sync", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_scoop_manifest_validates_and_syncs", func(t *testing.T) {
		// Exercises release.go's Scoop manifest validation + version/checksum
		// sync on the stable path: a well-formed bucket/erun.json must trace
		// `release: validating scoop manifest`, bump the version in the release
		// stage, and reach the curl/shasum checksum-sync trace (gated on
		// !DryRun). Locks the invariant guard's happy path.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, validScoopManifest)
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_scoop_manifest_validates_and_syncs", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_all_packaging_artifacts_syncs_them", func(t *testing.T) {
		// A stable release in a project shipping all three packaging
		// artifacts — Homebrew formula, Scoop manifest, marketplace.json.
		// The release stage must rewrite the formula's release-archive URL
		// (updateHomebrewFormulaReleaseVersion) alongside the scoop version
		// fields, and the sync-packaging-checksums stage must trace the
		// formula's curl/shasum (.tar.gz), the scoop curl/shasum (.zip),
		// and the marketplace `git rev-parse v<VERSION>^{}`, then git-add
		// all three files. The checksum downloads themselves are gated on
		// !DryRun, so the trace alone locks the wiring.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedHomebrewFormula(t, setup.Cwd)
		fixture.SeedScoopManifest(t, setup.Cwd, validScoopManifest)
		fixture.SeedMarketplaceJSON(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "add", "Formula", "bucket", ".claude-plugin")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add packaging artifacts")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_all_packaging_artifacts_syncs_them", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_invalid_scoop_manifest_fails", func(t *testing.T) {
		// A malformed bucket/erun.json (no mingw dependency, stale Fyne
		// wording instead of the MinGW/Wails prerequisite, missing
		// erun-app.exe) must fail the release during resolution — before any
		// git mutation — naming every violated invariant. Locks the guard that
		// keeps a broken Windows install recipe from being published.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, scoopManifestMissingMingwAndBin)
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for invalid scoop manifest, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_invalid_scoop_manifest_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_empty_scoop_script_fails", func(t *testing.T) {
		// An empty installer.script trips both the non-empty-script and the
		// MinGW-wording invariants, covering the remaining validation branches.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, scoopManifestEmptyScript)
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for empty scoop script, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_empty_scoop_script_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_malformed_scoop_manifest_fails", func(t *testing.T) {
		// A bucket/erun.json that is not valid JSON must fail the release
		// during the Scoop invariant validation (the unmarshal error branch
		// of checkScoopManifestInvariants), before any git mutation, naming
		// the manifest path and the parse failure.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, "{\n  \"version\": \"1.0.0\",\n")
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add malformed scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for malformed scoop manifest, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_malformed_scoop_manifest_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_release_tag_at_head_skips_tag_creation", func(t *testing.T) {
		// Re-running a release whose tag already points at HEAD must not
		// recreate the tag: canSkipExistingReleaseTag resolves v<VERSION>^{}
		// and HEAD to the same commit and the run traces "release tag
		// already exists at HEAD; skipping" instead of the `git tag`
		// command. Locks the re-run idempotency contract.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_release_tag_at_head_skips_tag_creation", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_stale_release_tag_without_force_fails", func(t *testing.T) {
		// The release tag exists on a commit other than HEAD and --force was
		// not passed: canSkipExistingReleaseTag must fail the release with
		// the tag/HEAD commit mismatch instead of silently retagging or
		// skipping. The --force variant above is the recovery path.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		// Advance HEAD past the tagged commit.
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for stale release tag without --force, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_stale_release_tag_without_force_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_version_file_at_project_root", func(t *testing.T) {
		// A project whose VERSION file sits at the project root (no nested
		// erun-devops module): resolveReleaseModuleRoot must use the project
		// root itself as the release root, and the docker-image discovery
		// must tolerate the missing docker/ directory (no images traced).
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "VERSION"), "2.5.0\n")
		fixture.RunGit(t, setup.Cwd, "add", "VERSION")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add version file")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_version_file_at_project_root", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_multiple_release_roots_fails", func(t *testing.T) {
		// Two nested modules with VERSION files make the release root
		// ambiguous: resolution must fail with "multiple release roots
		// found" and the dry-run trace must show the failed step. A third
		// VERSION under an assets/ subtree must NOT count as a candidate
		// (ignoredNestedReleaseRoot drops assets dirs), so the failure
		// names exactly the two real modules' ambiguity.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		for _, dir := range []string{
			filepath.Join(setup.Cwd, "alpha-devops"),
			filepath.Join(setup.Cwd, "beta-devops"),
			filepath.Join(setup.Cwd, "tools", "assets"),
		} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		mustWriteFile(t, filepath.Join(setup.Cwd, "alpha-devops", "VERSION"), "1.0.0\n")
		mustWriteFile(t, filepath.Join(setup.Cwd, "beta-devops", "VERSION"), "2.0.0\n")
		mustWriteFile(t, filepath.Join(setup.Cwd, "tools", "assets", "VERSION"), "3.0.0\n")
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add ambiguous release roots")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for ambiguous release roots, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_multiple_release_roots_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_force_includes_tag_deletion_for_stale_release_tag", func(t *testing.T) {
		// Exercises release.go --force path: when the release tag already
		// exists remotely on a commit other than HEAD, --dry-run must
		// trace tag deletion (local + origin) before recreating the tag.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		remoteRoot := filepath.Join(setup.Home, "origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "push", "-u", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "v1.4.2")
		// Advance HEAD past the tagged commit.
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "main")

		result := erun.Run(t, []string{"release", "--dry-run", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_force_includes_tag_deletion_for_stale_release_tag", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_skips_release_roots_in_gitignored_trees", func(t *testing.T) {
		// Regression for #398. The release-root walker used to descend
		// into third-party trees and treat every VERSION file as a
		// candidate. In a contribute clone where `yarn install` ran in
		// `erun-docs/`, `erun-docs/node_modules/lunr/VERSION` was
		// picked up alongside `erun-devops/VERSION`, and resolution
		// failed with "multiple release roots found under project
		// root". The walker now honors .gitignore (root + nested), so
		// any VERSION file in an ignored tree drops out of discovery.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		docsDir := filepath.Join(setup.Cwd, "erun-docs")
		if err := os.MkdirAll(filepath.Join(docsDir, "node_modules", "lunr"), 0o755); err != nil {
			t.Fatalf("mkdir node_modules/lunr: %v", err)
		}
		mustWriteFile(t, filepath.Join(docsDir, ".gitignore"), "node_modules\n")
		mustWriteFile(t, filepath.Join(docsDir, "node_modules", "lunr", "VERSION"), "2.3.9\n")
		fixture.RunGit(t, setup.Cwd, "add", "erun-docs/.gitignore")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "ignore node_modules in docs")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_skips_release_roots_in_gitignored_trees", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_untracked_file_reports_worktree_clean", func(t *testing.T) {
		// Regression for #400. release used to call
		// `git status --porcelain` with no flags and treat any output as
		// dirty, so an untracked .idea/ (or any other unignored
		// IDE/generator droppings) blocked release in a real run.
		// release publishes HEAD, not the worktree, so untracked files
		// must not gate the flow. The dry-run trace now exposes the
		// precondition outcome ("release: worktree clean = true") so
		// the integration suite can lock the behavior.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		if err := os.MkdirAll(filepath.Join(setup.Cwd, ".idea"), 0o755); err != nil {
			t.Fatalf("mkdir .idea: %v", err)
		}
		mustWriteFile(t, filepath.Join(setup.Cwd, ".idea", "workspace.xml"), "<project/>\n")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_with_untracked_file_reports_worktree_clean", normalize.Apply(result.Combined))
	})

	t.Run("real_run_emits_release_lifecycle_traces", func(t *testing.T) {
		// Real-run (no --dry-run) candidate release. Exercises the
		// `==> Releasing` / `==> Released ... in <ELAPSED>` umbrella
		// traces RunReleaseSpec emits via Info — the desktop activity
		// queue keys off them to light the sidebar spinner for a
		// standalone `erun release`. These lines are emitted only on a
		// real run, so dry-run goldens cannot cover them (mirrors
		// deploy_test.go's real_run_via_stubs and the build real-run
		// goldens). git is stubbed: resolution queries return canned
		// branch/commit, the tag-existence probe reports "not found" so
		// the tag is not skipped, and every mutation (fetch/rebase/
		// commit/tag/push) is a silent no-op — keeping the captured
		// output deterministic without a real remote or network.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo develop ;;
  *'rev-parse --short HEAD'*) echo abc1234 ;;
  *'^{}'*) exit 1 ;;
  *) exit 0 ;;
esac
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/real_run_emits_release_lifecycle_traces", normalize.Apply(result.Combined))
	})

	t.Run("real_run_force_stable_syncs_marketplace_and_bumps_version", func(t *testing.T) {
		// Real-run (no --dry-run) stable release with --force and a
		// marketplace.json. Three behaviors only a real run can prove:
		//   1. --force tag replacement actually executes the local
		//      `git tag -d` and remote `git push --delete` (both gated on
		//      !DryRun) when the tag exists locally and on origin;
		//   2. the sync-packaging-checksums stage resolves the release
		//      commit via `git rev-parse v<VERSION>^{}` and rewrites
		//      .claude-plugin/marketplace.json's source.sha on disk;
		//   3. the release/bump stage file updates really land: the chart
		//      gets the release version and VERSION gets the next patch.
		// git is stubbed (fixture.StubReleaseGit): resolution queries return
		// the canned main/abc1234, the tag probes report the stale tag at a
		// fixed commit, and every mutation is a silent no-op — keeping the
		// captured output deterministic without a real remote or network.
		// The on-disk file rewrites are asserted directly because they are
		// side effects outside the captured streams.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedMarketplaceJSON(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "add", ".claude-plugin")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add marketplace.json")
		const releaseSHA = "feedfacefeedfacefeedfacefeedfacefeedface"
		envVars := append(setup.Env(), fixture.StubReleaseGit(t, setup.Cwd+"/stubs", fixture.ReleaseGitStubSpec{
			Branch:      "main",
			ShortCommit: "abc1234",
			TagSHA:      releaseSHA,
			RemoteTag:   "v1.4.2",
		})...)

		result := erun.Run(t, []string{"release", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/real_run_force_stable_syncs_marketplace_and_bumps_version", normalize.Apply(result.Combined))

		marketplace, err := os.ReadFile(filepath.Join(setup.Cwd, ".claude-plugin", "marketplace.json"))
		if err != nil {
			t.Fatalf("read marketplace.json: %v", err)
		}
		if !strings.Contains(string(marketplace), `"sha": "`+releaseSHA+`"`) {
			t.Fatalf("marketplace.json source.sha not synced to release commit:\n%s", marketplace)
		}
		version, err := os.ReadFile(filepath.Join(setup.Cwd, "erun-devops", "VERSION"))
		if err != nil {
			t.Fatalf("read VERSION: %v", err)
		}
		if got := string(version); got != "1.4.3\n" {
			t.Fatalf("VERSION not bumped to next patch: %q", got)
		}
		chart, err := os.ReadFile(filepath.Join(setup.Cwd, "erun-devops", "k8s", "api", "Chart.yaml"))
		if err != nil {
			t.Fatalf("read Chart.yaml: %v", err)
		}
		if !strings.Contains(string(chart), "version: 1.4.2") {
			t.Fatalf("Chart.yaml not stamped with release version:\n%s", chart)
		}
	})

	t.Run("real_run_dirty_worktree_fails", func(t *testing.T) {
		// Real-run with a modified tracked file: the worktree-clean
		// precondition (waived in dry-run so audits work anywhere) must
		// fail the run before any stage executes, and the `==> Release
		// failed after <ELAPSED>` umbrella trace must close the lifecycle
		// the desktop's activity queue opened on `==> Releasing`. Real git
		// throughout: the dirty state is a real uncommitted change.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"), "FROM alpine:3.23\n")

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for dirty worktree, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/real_run_dirty_worktree_fails", normalize.Apply(result.Combined))
	})
}
