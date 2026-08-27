package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// validScoopManifest is the fixture that passes every release-time Scoop
// validation invariant.
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

// scoopManifestMissingMingwAndBin trips several Scoop validation invariants:
// no mingw dependency, stale Fyne wording, and a missing erun-app.exe.
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

// scoopManifestEmptyScript empties the installer script to trip the
// non-empty-script and MinGW-wording invariants.
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
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		golden.Equal(t, "release/dry_run_in_git_repo", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_develop_emits_candidate_plan", func(t *testing.T) {
		// On the develop branch, release resolves a candidate (rc) plan, not
		// a stable one.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_develop_emits_candidate_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_develop_emits_stable_plan", func(t *testing.T) {
		// On main with a develop branch present, a stable release syncs and
		// pushes both main and develop.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "branch", "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_develop_emits_stable_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_without_develop_pushes_only_main", func(t *testing.T) {
		// Without a develop branch, a stable release syncs and pushes only
		// main.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_without_develop_pushes_only_main", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_includes_linux_release_scripts", func(t *testing.T) {
		// Linux release scripts run only where package builds are supported,
		// which erun reads as a linux host that has dpkg-deb. Both halves are
		// declared here — ERUN_HOST_OS_OVERRIDE for the host, a dpkg-deb stub on
		// PATH for the tool — so the golden is the same on every host instead of
		// describing whichever machine recorded it. The build-only second
		// component (build.sh, no release.sh) is a valid linux context that
		// contributes no release script, exercising the skip-component branch.
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

		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinary(t, stubs, "dpkg-deb", "")
		envVars := append(releaseEnv(t, setup),
			"ERUN_HOST_OS_OVERRIDE=linux",
			// The support check resolves dpkg-deb through exec.LookPath, so the
			// stub has to be reachable on PATH rather than via ERUN_..._BIN.
			"PATH="+stubs+string(os.PathListSeparator)+setup.PathDir,
		)
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_includes_linux_release_scripts", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_darwin_host_skips_linux_release_scripts", func(t *testing.T) {
		// On a host that cannot build linux packages, release must trace the
		// skip decision rather than silently omit the scripts.
		// ERUN_HOST_OS_OVERRIDE=darwin pins the unsupported branch so the
		// golden is deterministic on every host, including the Linux build gate
		// where the support check would otherwise pass.
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

		envVars := append(releaseEnv(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_darwin_host_skips_linux_release_scripts", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_marketplace_emits_sha_sync", func(t *testing.T) {
		// When the project ships a .claude-plugin/marketplace.json, release
		// must trace the packaging-checksum sync for it. The bump is gated on
		// !DryRun, so in dry-run the trace alone proves the wiring.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedMarketplaceJSON(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "add", ".claude-plugin")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add marketplace.json")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
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
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_scoop_manifest_validates_and_syncs", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_all_packaging_artifacts_syncs_them", func(t *testing.T) {
		// A stable release in a project shipping all three packaging artifacts
		// — Homebrew formula, Scoop manifest, marketplace.json — must sync the
		// version and checksum for each. The checksum downloads are gated on
		// !DryRun, so the trace alone locks the wiring.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedHomebrewFormula(t, setup.Cwd)
		fixture.SeedScoopManifest(t, setup.Cwd, validScoopManifest)
		fixture.SeedMarketplaceJSON(t, setup.Cwd)
		fixture.RunGit(t, setup.Cwd, "add", "Formula", "bucket", ".claude-plugin")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add packaging artifacts")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_all_packaging_artifacts_syncs_them", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_invalid_scoop_manifest_fails", func(t *testing.T) {
		// An invalid Scoop manifest must fail the release during resolution,
		// before any git mutation, naming every violated invariant — the guard
		// that keeps a broken Windows install recipe from being published.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, scoopManifestMissingMingwAndBin)
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for invalid scoop manifest, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_invalid_scoop_manifest_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_empty_scoop_script_fails", func(t *testing.T) {
		// Covers the remaining Scoop validation branches.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, scoopManifestEmptyScript)
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for empty scoop script, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_empty_scoop_script_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_malformed_scoop_manifest_fails", func(t *testing.T) {
		// Non-JSON manifest content must fail during Scoop validation, before
		// any git mutation.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.SeedScoopManifest(t, setup.Cwd, "{\n  \"version\": \"1.0.0\",\n")
		fixture.RunGit(t, setup.Cwd, "add", "bucket")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add malformed scoop manifest")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for malformed scoop manifest, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_malformed_scoop_manifest_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_release_tag_at_head_skips_tag_creation", func(t *testing.T) {
		// Re-running a release whose tag already points at HEAD must skip tag
		// creation rather than recreate it — the re-run idempotency contract.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_release_tag_at_head_skips_tag_creation", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_stale_release_tag_without_force_fails", func(t *testing.T) {
		// A stale release tag (on a commit other than HEAD) without --force
		// must fail on the tag/HEAD mismatch rather than silently retag or
		// skip; the --force variant above is the recovery path.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for stale release tag without --force, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_stale_release_tag_without_force_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_version_file_at_project_root", func(t *testing.T) {
		// When the VERSION file sits at the project root (no nested devops
		// module), the project root itself is the release root, and a missing
		// docker/ directory is tolerated (no images traced).
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		mustWriteFile(t, filepath.Join(setup.Cwd, "VERSION"), "2.5.0\n")
		fixture.RunGit(t, setup.Cwd, "add", "VERSION")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add version file")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_version_file_at_project_root", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_multiple_release_roots_fails", func(t *testing.T) {
		// Two nested modules with VERSION files make the release root
		// ambiguous and must fail; a third VERSION under an assets/ subtree
		// must not count (assets dirs are excluded), so the failure names
		// exactly the two real modules.
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
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for ambiguous release roots, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_multiple_release_roots_fails", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_force_includes_tag_deletion_for_stale_release_tag", func(t *testing.T) {
		// With --force and a release tag that already exists remotely on a
		// commit other than HEAD, the plan must trace deleting the tag (local
		// + origin) before recreating it.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		remoteRoot := filepath.Join(setup.Home, "origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "push", "-u", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "v1.4.2")
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "main")

		result := erun.Run(t, []string{"release", "--dry-run", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_force_includes_tag_deletion_for_stale_release_tag", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_skips_release_roots_in_gitignored_trees", func(t *testing.T) {
		// Regression. The release-root walker used to descend
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

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_skips_release_roots_in_gitignored_trees", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_untracked_file_reports_worktree_clean", func(t *testing.T) {
		// Regression. release used to call
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

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_with_untracked_file_reports_worktree_clean", normalize.Apply(result.Combined))
	})

	t.Run("real_run_emits_release_lifecycle_traces", func(t *testing.T) {
		// Real-run (no --dry-run) candidate release. The `==> Releasing` /
		// `==> Released ... in <ELAPSED>` umbrella traces are emitted only on
		// a real run, so dry-run goldens cannot cover them; the desktop
		// activity queue keys off them to light the sidebar spinner for a
		// standalone `erun release`. git is stubbed so the tag is not skipped
		// and every mutation is a no-op, keeping output deterministic without
		// a real remote.
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
		envVars = append(envVars, stubPublishToolchain(t, setup)...)
		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/real_run_emits_release_lifecycle_traces", normalize.Apply(result.Combined))
	})

	t.Run("real_run_force_stable_syncs_marketplace_and_bumps_version", func(t *testing.T) {
		// Real-run (no --dry-run) stable release with --force and a
		// marketplace.json. Three behaviors only a real run can prove:
		//   1. --force tag replacement runs the local `git tag -d` and remote
		//      `git push --delete` when the tag exists locally and on origin;
		//   2. the checksum-sync stage resolves the release commit and
		//      rewrites marketplace.json's source.sha on disk;
		//   3. the bump-stage file updates land: the chart gets the release
		//      version and VERSION gets the next patch.
		// git is stubbed so the tag probes report a stale tag and every
		// mutation is a no-op, keeping output deterministic without a real
		// remote. The on-disk rewrites are asserted directly because they are
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
		envVars = append(envVars, stubPublishToolchain(t, setup)...)

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

	t.Run("real_run_refuses_when_the_docker_root_is_nearly_full", func(t *testing.T) {
		// The release that fills the node's disk is the one most likely
		// to get evicted by it, so low headroom at the docker root refuses the
		// release before the build spends anything — the same "known failure
		// caught up front" shape as the registry-permission preflight. docker
		// and df are both stubbed to report a real (if fake) low-space
		// filesystem, exercising the conclusive refusal branch rather than the
		// "not observable" fallback the other release scenarios take.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)
		dockerRoot := filepath.Join(setup.Cwd, "fake-docker-root")
		if err := os.MkdirAll(dockerRoot, 0o755); err != nil {
			t.Fatalf("mkdir fake docker root: %v", err)
		}
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1 $2" in`,
			`  "info -f") printf '%s' '` + dockerRoot + `' ;;`,
			`  "builder prune") exit 0 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		// 1 GiB available, well under the 20 GiB default floor.
		fixture.StubBinaryAdvanced(t, stubs, "df", fixture.StubBinarySpec{
			Stdout: "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
				"overlay          104857600 93763584   1048576      99% " + dockerRoot + "\n",
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "df")...)

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for low disk headroom, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "only 1.0 GiB free at the docker root, below the 20.0 GiB a multi-arch release build needs") {
			t.Fatalf("expected the disk-headroom refusal in output:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "filling this disk is what evicts the pod running the release") {
			t.Fatalf("expected the eviction-risk explanation in output:\n%s", result.Combined)
		}

		// Outside the captured streams: the refusal must leave the version
		// file on the version it was releasing, so re-running retries it.
		assertVersionFile(t, setup, "1.4.2\n")
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

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for dirty worktree, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/real_run_dirty_worktree_fails", normalize.Apply(result.Combined))
	})

	t.Run("real_run_unpushed_unreachable_stale_tag_names_the_leftover_and_its_remedy", func(t *testing.T) {
		// A previous release run tagged a commit and was interrupted
		// before it pushed anything (e.g. the pod holding the worktree was
		// replaced) — this run's own worktree has since moved past that
		// commit, so origin/main never saw it either. That is a safely
		// reclaimable leftover, not a real tag collision, so the refusal
		// names the diagnosis and the exact remedy instead of only "already
		// exists at <sha>, expected HEAD <sha>".
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "orphaned release stamp")
		fixture.RunGit(t, setup.Cwd, "tag", "v1.4.2")
		fixture.RunGit(t, setup.Cwd, "reset", "-q", "--hard", "HEAD~1")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for the stale tag, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, `a previous run left an unpushed local tag "v1.4.2"`) {
			t.Fatalf("expected the leftover-tag diagnosis in output:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "delete it with `git tag -d v1.4.2` to retry") {
			t.Fatalf("expected the exact remedy command in output:\n%s", result.Combined)
		}
	})

	t.Run("real_run_a_tag_collision_reachable_from_origin_gets_no_reclaim_suggestion", func(t *testing.T) {
		// The mirror case: the tag's commit already reached origin/main (a
		// genuine prior release, not a local leftover), so suggesting `git tag
		// -d` would offer to discard part of the published, agreed history.
		// The refusal must stay the plain "already exists" message.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)

		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "a real prior release")
		fixture.RunGit(t, setup.Cwd, "tag", "v1.4.2")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "unrelated local work")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for the real tag collision, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, `release tag "v1.4.2" already exists at`) {
			t.Fatalf("expected the plain already-exists refusal in output:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "delete it with") {
			t.Fatalf("must not suggest reclaiming a tag origin has already incorporated:\n%s", result.Combined)
		}
	})

	t.Run("real_run_publishes_before_the_tag_reaches_origin", func(t *testing.T) {
		// Regression. A release used to run only its git stages: it
		// tagged, announced, and bumped the version without ever building or
		// publishing an image or a chart, so the announced version was
		// undeployable and the gap only surfaced at the next deploy's chart
		// pull. Release now publishes between its local stages and its
		// outward-facing ones. Real git against a bare origin proves the
		// ordering behaviourally: the publish stage runs, the read-back
		// verification resolves each image and chart, and only then does the
		// tag reach origin. docker and helm are stubbed because the harness has
		// no daemon or registry; git is real so "public" means public.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubPublishToolchain(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/real_run_publishes_before_the_tag_reaches_origin", normalize.Apply(result.Combined))

		if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
			t.Fatalf("release tag did not reach origin after a successful publish:\n%s", tags)
		}
		assertVersionFile(t, setup, "1.4.3\n")
	})

	t.Run("real_run_publish_failure_leaves_no_public_tag", func(t *testing.T) {
		// The other half of the same contract: when the publish cannot
		// complete, the release must fail while nothing is public. docker fails
		// every invocation, so the build inside the publish stage errors out.
		// The tag must not exist on origin and VERSION must still hold the
		// version that was being released, so re-running retries the same
		// version rather than stranding it behind a bump.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{Stderr: "simulated docker failure", ExitCode: 1})
		fixture.StubBinary(t, stubs, "helm", "")

		// #1201: give the registry-credential preflight a resolvable credential
		// so this scenario still reaches the simulated docker failure it is about.
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		envVars = append(envVars, "GH_TOKEN=integration-test-token")
		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the publish fails, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/real_run_publish_failure_leaves_no_public_tag", normalize.Apply(result.Combined))

		if tags := remoteTags(t, setup); strings.Contains(tags, "refs/tags/v1.4.2") {
			t.Fatalf("a failed release pushed its tag to origin:\n%s", tags)
		}
		assertVersionFile(t, setup, "1.4.2\n")
	})

	t.Run("real_run_refuses_when_the_base_branch_moved_before_the_build", func(t *testing.T) {
		// Regression. A release established that it could fast-forward
		// once, in sync-remote, and then spent the whole build before pushing
		// anything; a base branch that moved in between was discovered at the
		// final push, with everything already public. The branch is now re-read
		// immediately before the build, and a release that cannot land refuses
		// while nothing is published and the version file is untouched.
		//
		// git is stubbed so the re-read reports a moved branch deterministically:
		// the move is what this scenario is about, and no fixture can make a real
		// remote move between two of the release's own git calls.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		stubs := filepath.Join(setup.Cwd, "stubs")
		fixture.StubBinaryWithScript(t, stubs, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo main ;;
  *'rev-list --count HEAD..FETCH_HEAD'*) echo 3 ;;
  *'rev-parse --short HEAD'*) echo abc1234 ;;
  *'show-ref --verify --quiet'*) exit 1 ;;
  *'^{}'*) exit 1 ;;
  *) : ;;
esac
exit 0
`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
		envVars = append(envVars, stubPublishToolchain(t, setup)...)

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the base branch moved before the build, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/real_run_refuses_when_the_base_branch_moved_before_the_build", normalize.Apply(result.Combined))

		// Outside the captured streams: the refusal must leave the version file
		// on the version it was releasing, so re-running retries that version.
		assertVersionFile(t, setup, "1.4.2\n")
	})

	t.Run("real_run_absorbs_a_base_branch_that_moved_during_the_build", func(t *testing.T) {
		// Regression, the other half. Releasing 1.0.176, a pull request
		// merged to main while the release was building: the images, the charts
		// and the tag all went public, and then the final push was rejected, so
		// the repository carried neither the packaging commit nor the version
		// bump and VERSION still read the version just published. The push now
		// rebases onto the moved branch and retries.
		//
		// The helm stub is what moves origin. helm first runs inside the publish
		// stage, which places the move exactly where the real one landed — after
		// the pre-build re-check, while the version is going public.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		origin := seedBareOrigin(t, setup)

		// The linux release script is where the GitHub release entry is created,
		// and it runs after the push. A marker proves a rejected push no longer
		// strands it.
		releaseScriptRan := filepath.Join(setup.Home, "github-release-created")
		linuxComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli")
		if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"),
			[]byte("#!/bin/sh\n: > '"+filepath.ToSlash(releaseScriptRan)+"'\n"), 0o755); err != nil {
			t.Fatalf("write release.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add linux release script")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

		// The checkout that stands in for the pull request, cloned once origin is
		// at the commit the release starts from. -b main because the bare origin's
		// HEAD still names whatever branch git init defaults to, so a plain clone
		// would land on an unborn branch with no main to push.
		merging := filepath.Join(setup.Home, "merging")
		fixture.RunGit(t, setup.Home, "clone", "-q", "-b", "main", origin, merging)
		fixture.RunGit(t, merging, "config", "user.email", "test@example")
		fixture.RunGit(t, merging, "config", "user.name", "Test")

		stubs := filepath.Join(setup.Cwd, "stubs")
		stubDockerWithManifestTracking(t, stubs)
		fixture.StubBinaryMergingIntoRemoteOnce(t, stubs, "helm", merging, "main", "a merged pull request")
		fixture.StubBinary(t, stubs, "dpkg-deb", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		envVars = append(envVars,
			// Linux package builds are what carry the GitHub release step, so the
			// host and the tool are both declared rather than left to the runner.
			"ERUN_HOST_OS_OVERRIDE=linux",
			"PATH="+stubs+string(os.PathListSeparator)+setup.PathDir,
			// #1201: give the registry-credential preflight a resolvable credential
			// so this scenario still reaches the moved-branch-absorption it is about.
			"GH_TOKEN=integration-test-token",
		)

		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// git's push-rejection hint block is worded differently across git
		// versions, so it cannot be part of a reviewed golden; the `! [rejected]`
		// line and erun's own retry line are what this scenario locks.
		combined := normalize.Apply(result.Combined, normalize.Replacement{Pattern: regexp.MustCompile(`(?m)^hint:.*\n?`), Token: ""})
		golden.Equal(t, "release/real_run_absorbs_a_base_branch_that_moved_during_the_build",
			canonicalizeReleaseStageTimingOrder(t, combined))

		// Side effects outside the captured streams, one per contract the
		// absorbed push has to hold: the repository records the release it
		// published, the tag is public, the version moved past what was
		// published, and the GitHub release step still ran.
		remote := remoteMainSubjects(t, merging)
		for _, want := range []string{"a merged pull request", "[skip ci] release 1.4.2", "[skip ci] prepare 1.4.3"} {
			if !strings.Contains(remote, want) {
				t.Fatalf("origin/main is missing %q after the absorbed push:\n%s", want, remote)
			}
		}
		if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
			t.Fatalf("release tag did not reach origin after the absorbed push:\n%s", tags)
		}
		assertVersionFile(t, setup, "1.4.3\n")
		if _, err := os.Stat(releaseScriptRan); err != nil {
			t.Fatalf("the linux release script never ran, so the GitHub release entry was stranded: %v", err)
		}
	})

	t.Run("dry_run_refuses_to_release_an_image_it_would_not_publish", func(t *testing.T) {
		// Run from inside one component's build context, release resolves only
		// that component's build but still stamps and would tag every image on
		// the release version line. That is the shape of the original defect —
		// a version announced for artifacts nobody publishes — so it is refused
		// during resolution, before any stage runs.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		webDir := filepath.Join(setup.Cwd, "erun-devops", "docker", "web")
		if err := os.MkdirAll(webDir, 0o755); err != nil {
			t.Fatalf("mkdir web component dir: %v", err)
		}
		mustWriteFile(t, filepath.Join(webDir, "Dockerfile"), "FROM alpine:3.22\n")
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add web component")

		componentCwd := filepath.Join(setup.Cwd, "erun-devops", "docker", "api")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: componentCwd, Env: releaseEnv(t, setup)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a release that cannot publish every image, got 0: %s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_refuses_to_release_an_image_it_would_not_publish", normalize.Apply(result.Combined))
	})
}

// releaseEnv declares the docker stub every release scenario now needs: release
// resolves the build execution that publishes the version, so it inspects the
// local image store for fingerprint cache tags. The stub reports no local image
// so the plan consistently shows a rebuild.
func releaseEnv(t *testing.T, setup env.Setup) []string {
	t.Helper()
	return append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
}

// stubDockerWithManifestTracking declares a docker stub that succeeds on
// everything except `manifest inspect`, which it answers from real
// (marker-file-backed) state rather than a blanket yes: it backs both the
// pre-publish "already published?" probe and the post-publish verify
// step, so a stub that always answered yes would make a first-ever-release
// scenario falsely report the image as already published before publishing
// anything. Reporting false until `manifest push` actually runs, then true,
// keeps the golden honest about what the scenario models.
func stubDockerWithManifestTracking(t *testing.T, stubs string) {
	t.Helper()
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1 $2" in`,
		`  "manifest inspect")`,
		`    marker="` + stubs + `/manifest-published-$(printf '%s' "$3" | tr '/:' '__')"`,
		`    [ -f "$marker" ] && exit 0 || exit 1`,
		`    ;;`,
		`  "manifest push")`,
		`    marker="` + stubs + `/manifest-published-$(printf '%s' "$3" | tr '/:' '__')"`,
		`    touch "$marker"`,
		`    exit 0`,
		`    ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
}

// stubPublishToolchain declares succeeding docker and helm stubs so a real-run
// release can execute its publish stage — build, push, manifest assembly, chart
// publish, and the read-back verification — without a daemon or a registry.
func stubPublishToolchain(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDockerWithManifestTracking(t, stubs)
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := fixture.StubEnv(stubs, "docker", "helm")
	// #1201: the registry-credential preflight refuses up front when no
	// ghcr.io credential resolves at all. A real publish toolchain always has
	// one; GH_TOKEN is the fixture-side stand-in so these scenarios keep
	// exercising what happens once that check passes.
	return append(envVars, "GH_TOKEN=integration-test-token")
}

// seedBareOrigin gives a release scenario a real remote, so "the tag is public"
// is an observable fact rather than a stubbed git call that returned zero. It
// returns the remote's path for scenarios that also need a second checkout of it.
func seedBareOrigin(t *testing.T, setup env.Setup) string {
	t.Helper()
	remoteRoot := filepath.Join(setup.Home, "origin.git")
	fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
	fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "main")
	return remoteRoot
}

// releaseStageTimingSiblingsToCanonicalize are this scenario's top-level
// release-stage timing rows other than "publish", the one stage whose
// duration (a multi-arch image build plus two chart publishes) always
// dominates by a wide, host-independent margin. These five run sequentially
// and each does at most a couple of real git subprocess calls, except
// "push": this scenario's whole point is a rejected push that rebases and
// retries, so "push" alone does several more real git subprocess calls than
// its siblings. erun-common/timing.go's orderedTimingRows sorts by measured
// duration, tie-breaking by name only when two rows land within a fixed
// noise floor of each other — so whether "push"'s extra real git latency
// crosses that floor and reorders it ahead of its siblings depends on how
// fast git subprocesses run on the host, not on anything this scenario
// controls. Canonicalizing this block's order before comparing keeps the
// golden asserting what is actually stable: which five stages ran and
// roughly how long each took, not which one happened to be a few git calls
// slower on this particular machine.
var releaseStageTimingSiblingsToCanonicalize = []string{
	"post-release-version-bump", "push", "release", "sync-remote", "verify-publication",
}

// canonicalizeReleaseStageTimingOrder reorders the contiguous
// releaseStageTimingSiblingsToCanonicalize block within a normalized
// step-timing table into a fixed, alphabetical order, so the golden compares
// something that does not depend on real subprocess timing. Called after
// normalize.Apply, so every row already reads "<name> [<ELAPSED>]". Fails the
// test loudly, rather than silently comparing the wrong thing, if the block
// is not exactly the expected five rows in a row — a sign the production
// output's shape changed and this helper needs to change with it.
func canonicalizeReleaseStageTimingOrder(t *testing.T, output string) string {
	t.Helper()
	wantCount := len(releaseStageTimingSiblingsToCanonicalize)
	want := make(map[string]bool, wantCount)
	for _, name := range releaseStageTimingSiblingsToCanonicalize {
		want["    "+name+" [<ELAPSED>]"] = true
	}
	lines := strings.Split(output, "\n")
	start := -1
	for i, line := range lines {
		if want[line] {
			start = i
			break
		}
	}
	if start == -1 || start+wantCount > len(lines) {
		t.Fatalf("release stage timing siblings not found in:\n%s", output)
	}
	block := lines[start : start+wantCount]
	seen := make(map[string]bool, wantCount)
	for _, line := range block {
		if !want[line] || seen[line] {
			t.Fatalf("release stage timing siblings not found as the expected contiguous block in:\n%s", output)
		}
		seen[line] = true
	}
	sort.Strings(block)
	return strings.Join(lines, "\n")
}

// remoteMainSubjects reads origin/main's commit subjects through a second
// checkout, so a scenario can assert what the release actually landed on the
// remote rather than what its own worktree believes.
func remoteMainSubjects(t *testing.T, repoDir string) string {
	t.Helper()
	fixture.RunGit(t, repoDir, "fetch", "-q", "origin")
	cmd := exec.Command("git", "log", "--format=%s", "origin/main")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log origin/main: %v: %s", err, output)
	}
	return string(output)
}

func remoteTags(t *testing.T, setup env.Setup) string {
	t.Helper()
	cmd := exec.Command("git", "ls-remote", "--tags", "origin")
	cmd.Dir = setup.Cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-remote --tags origin: %v: %s", err, output)
	}
	return string(output)
}

func assertVersionFile(t *testing.T, setup env.Setup, want string) {
	t.Helper()
	version, err := os.ReadFile(filepath.Join(setup.Cwd, "erun-devops", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got := string(version); got != want {
		t.Fatalf("VERSION is %q, want %q", got, want)
	}
}
