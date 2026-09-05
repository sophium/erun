package integration

import (
	"os"
	"os/exec"
	"path/filepath"
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
	t.Parallel()
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

	t.Run("dry_run_main_with_remote_only_develop_emits_stable_plan", func(t *testing.T) {
		// Regression for erun#2013. A checkout that only ever fetched
		// origin/develop as a remote-tracking ref (no local `develop` branch)
		// must still get the stable release's sync-develop and both-branch
		// push stages: `git checkout develop` resolves a remote-only ref into
		// a new local tracking branch on its own, so the stages run fine —
		// checking refs/heads alone used to treat this the same as "no
		// develop branch exists" and silently dropped both stages with no
		// trace and no refusal.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		remoteRoot := filepath.Join(setup.Home, "origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "push", "-q", "-u", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "-b", "develop")
		fixture.RunGit(t, setup.Cwd, "push", "-q", "-u", "origin", "develop")
		fixture.RunGit(t, setup.Cwd, "checkout", "-q", "main")
		fixture.RunGit(t, setup.Cwd, "branch", "-q", "-D", "develop")
		fixture.RunGit(t, setup.Cwd, "fetch", "-q", "origin")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "sync-develop") {
			t.Fatalf("expected the sync-develop stage in a plan for a remote-only develop branch:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "push --follow-tags origin main develop") {
			t.Fatalf("expected the push stage to include develop for a remote-only develop branch:\n%s", result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_remote_only_develop_emits_stable_plan", normalize.Apply(result.Combined))
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

	t.Run("dry_run_in_a_runtime_pod_claims_the_release_version_lease", func(t *testing.T) {
		// Inside a runtime pod (ERUN_TENANT/ERUN_ENVIRONMENT injected by the
		// chart), release claims an exclusive, version-scoped activity lease
		// before doing anything else — the erun#1619 fix for two orchestrators
		// racing the same release. No other release scenario sets these two
		// vars, so this is the only golden that shows the claim trace; every
		// other release scenario is exercising the off-pod, single-caller path
		// where the claim is a no-op.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		envVars := append(releaseEnv(t, setup), "ERUN_TENANT=erun", "ERUN_ENVIRONMENT=build")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_in_a_runtime_pod_claims_the_release_version_lease", normalize.Apply(result.Combined))
	})

	t.Run("real_run_refuses_when_another_orchestrator_is_already_releasing_the_version", func(t *testing.T) {
		// Regression for erun#1619: two orchestrators driving the same runtime
		// pod could both pass every preflight and race the same release, the
		// loser discovering the collision only via a tag mismatch after minutes
		// of wasted publishing. Pre-claiming the exact exclusive lease scope
		// release itself takes — via the activity lease CLI, sharing this
		// scenario's XDG_CACHE_HOME — simulates "another orchestrator got there
		// first". release must refuse before it ever touches git — sync-remote
		// never runs — though execution-plan resolution has already made its
		// own read-only docker fingerprint check by this point, hence the
		// docker stub.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")

		blocker := erun.Run(t, []string{
			"activity", "lease", "take", "--tenant", "erun", "--environment", "build",
			"--name", "blocker", "--exclusive", "--scope", "release-version:1.4.2",
			"--orchestrator", "existing-holder",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
		if blocker.ExitCode != 0 {
			t.Fatalf("pre-claim: exit %d: %s", blocker.ExitCode, blocker.Combined)
		}

		envVars := append(releaseEnv(t, setup), "ERUN_TENANT=erun", "ERUN_ENVIRONMENT=build")
		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the version is already being released, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "1.4.2 is already being released by orchestrator existing-holder") {
			t.Fatalf("expected the refusal to name the version and the holder:\n%s", result.Combined)
		}

		// Outside the captured streams: the refusal must leave the version file
		// untouched, so re-running retries once the other release finishes (or
		// its claim is reclaimed automatically).
		assertVersionFile(t, setup, "1.4.2\n")
	})

	t.Run("real_run_never_touches_docker_or_helm", func(t *testing.T) {
		// Regression: erun release is version paperwork only and must never
		// build, publish, or verify an artifact. No docker or helm binary is
		// declared on PATH at all here
		// (unlike releaseEnv's siblings, which never needed one either but
		// this is the scenario that makes the absence the point) — if
		// release regressed back to resolving a build execution and tried to
		// invoke either, the run would fail with "executable file not found"
		// instead of completing the plain git lifecycle.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		seedBareOrigin(t, setup)
		result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, unwanted := range []string{"docker build", "docker push", "docker manifest", "helm package", "helm push", "helm pull", "stage: publish", "stage: verify-publication", "Verified published", "Verified anonymously pullable"} {
			if strings.Contains(result.Combined, unwanted) {
				t.Fatalf("erun release must never build or publish anything; found %q in output:\n%s", unwanted, result.Combined)
			}
		}
		if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
			t.Fatalf("release tag did not reach origin:\n%s", tags)
		}
		assertVersionFile(t, setup, "1.4.3\n")
	})
}

// releaseEnv is the shared env for release scenarios. release itself never
// touches docker (it discovers what it would build/publish by scanning the
// filesystem, not by asking the daemon), so no docker stub is required here
// — unlike build --release, which resolves a real build execution.
func releaseEnv(t *testing.T, setup env.Setup) []string {
	t.Helper()
	return setup.Env()
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
