package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
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
	t.Run("help", testReleaseHelp)
	t.Run("dry_run_in_git_repo", testReleaseDryRunInGitRepo)
	t.Run("dry_run_develop_emits_candidate_plan", testReleaseDryRunDevelopEmitsCandidatePlan)
	t.Run("dry_run_main_with_develop_emits_stable_plan", testReleaseDryRunMainWithDevelopEmitsStablePlan)
	t.Run("dry_run_main_with_remote_only_develop_emits_stable_plan", testReleaseDryRunMainWithRemoteOnlyDevelopEmitsStablePlan)
	t.Run("dry_run_main_without_develop_pushes_only_main", testReleaseDryRunMainWithoutDevelopPushesOnlyMain)
	t.Run("dry_run_build_release_exports_and_uploads_linux_packages", testReleaseDryRunBuildReleaseExportsAndUploadsLinuxPackages)
	t.Run("dry_run_linux_package_without_dist_stage_is_refused", testReleaseDryRunLinuxPackageWithoutDistStageIsRefused)
	t.Run("dry_run_main_with_marketplace_emits_sha_sync", testReleaseDryRunMainWithMarketplaceEmitsShaSync)
	t.Run("dry_run_main_with_scoop_manifest_validates_and_syncs", testReleaseDryRunMainWithScoopManifestValidatesAndSyncs)
	t.Run("dry_run_main_with_all_packaging_artifacts_syncs_them", testReleaseDryRunMainWithAllPackagingArtifactsSyncsThem)
	t.Run("dry_run_main_traces_anonymous_pullability_probe_for_terraform_referenced_image", testReleaseDryRunMainTracesAnonymousPullabilityProbeForTerraformReferencedImage)
	t.Run("dry_run_main_with_invalid_scoop_manifest_fails", testReleaseDryRunMainWithInvalidScoopManifestFails)
	t.Run("dry_run_main_with_empty_scoop_script_fails", testReleaseDryRunMainWithEmptyScoopScriptFails)
	t.Run("dry_run_main_with_malformed_scoop_manifest_fails", testReleaseDryRunMainWithMalformedScoopManifestFails)
	t.Run("dry_run_release_tag_at_head_skips_tag_creation", testReleaseDryRunReleaseTagAtHeadSkipsTagCreation)
	t.Run("dry_run_stale_release_tag_without_force_fails", testReleaseDryRunStaleReleaseTagWithoutForceFails)
	t.Run("dry_run_version_file_at_project_root", testReleaseDryRunVersionFileAtProjectRoot)
	t.Run("dry_run_multiple_release_roots_fails", testReleaseDryRunMultipleReleaseRootsFails)
	t.Run("dry_run_force_includes_tag_deletion_for_stale_release_tag", testReleaseDryRunForceIncludesTagDeletionForStaleReleaseTag)
	t.Run("dry_run_skips_release_roots_in_gitignored_trees", testReleaseDryRunSkipsReleaseRootsInGitignoredTrees)
	t.Run("dry_run_with_untracked_file_reports_worktree_clean", testReleaseDryRunWithUntrackedFileReportsWorktreeClean)
	t.Run("real_run_emits_release_lifecycle_traces", testReleaseRealRunEmitsReleaseLifecycleTraces)
	t.Run("real_run_force_stable_syncs_marketplace_and_bumps_version", testReleaseRealRunForceStableSyncsMarketplaceAndBumpsVersion)
	t.Run("real_run_refuses_when_the_docker_root_is_nearly_full", testReleaseRealRunRefusesWhenTheDockerRootIsNearlyFull)
	t.Run("real_run_refusal_does_not_name_a_docker_remedy_docker_cannot_perform", testReleaseRealRunRefusalDoesNotNameADockerRemedyDockerCannotPerform)
	t.Run("real_run_dirty_worktree_fails", testReleaseRealRunDirtyWorktreeFails)
	t.Run("real_run_unpushed_unreachable_stale_tag_names_the_leftover_and_its_remedy", testReleaseRealRunUnpushedUnreachableStaleTagNamesTheLeftoverAndItsRemedy)
	t.Run("real_run_a_tag_collision_reachable_from_origin_gets_no_reclaim_suggestion", testReleaseRealRunATagCollisionReachableFromOriginGetsNoReclaimSuggestion)
	t.Run("real_run_publishes_before_the_tag_reaches_origin", testReleaseRealRunPublishesBeforeTheTagReachesOrigin)
	t.Run("real_run_publish_failure_leaves_no_public_tag", testReleaseRealRunPublishFailureLeavesNoPublicTag)
	t.Run("real_run_refuses_when_the_base_branch_moved_before_the_build", testReleaseRealRunRefusesWhenTheBaseBranchMovedBeforeTheBuild)
	t.Run("real_run_absorbs_a_base_branch_that_moved_during_the_build", testReleaseRealRunAbsorbsABaseBranchThatMovedDuringTheBuild)
	t.Run("real_run_names_the_rejected_develop_and_does_not_rebase_main", testReleaseRealRunNamesTheRejectedDevelopAndDoesNotRebaseMain)
	t.Run("dry_run_in_a_runtime_pod_claims_the_release_version_lease", testReleaseDryRunInARuntimePodClaimsTheReleaseVersionLease)
	t.Run("real_run_refuses_when_another_orchestrator_is_already_releasing_the_version", testReleaseRealRunRefusesWhenAnotherOrchestratorIsAlreadyReleasingTheVersion)
	t.Run("dry_run_refuses_to_release_an_image_it_would_not_publish", testReleaseDryRunRefusesToReleaseAnImageItWouldNotPublish)
}

func testReleaseHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"release", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/help", normalize.Apply(result.Combined))
}

func testReleaseDryRunInGitRepo(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	golden.Equal(t, "release/dry_run_in_git_repo", normalize.Apply(result.Combined))
}

func testReleaseDryRunDevelopEmitsCandidatePlan(t *testing.T) {
	// On the develop branch, release resolves a candidate (rc) plan, not
	// a stable one.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_develop_emits_candidate_plan", normalize.Apply(result.Combined))
}

func testReleaseDryRunMainWithDevelopEmitsStablePlan(t *testing.T) {
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
}

func testReleaseDryRunMainWithRemoteOnlyDevelopEmitsStablePlan(t *testing.T) {
	// A checkout that only ever fetched
	// origin/develop as a remote-tracking ref (no local `develop` branch)
	// must still get the stable release's sync-develop and both-branch
	// push stages: `git checkout develop` resolves a remote-only ref into
	// a new local tracking branch on its own, so checking refs/heads alone
	// would silently drop both stages.
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
}

func testReleaseDryRunMainWithoutDevelopPushesOnlyMain(t *testing.T) {
	// Without a develop branch, a stable release syncs and pushes only
	// main.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_main_without_develop_pushes_only_main", normalize.Apply(result.Combined))
}

func testReleaseDryRunBuildReleaseExportsAndUploadsLinuxPackages(t *testing.T) {
	// A release builds every linux package's `dist` stage for both release
	// platforms inside the publish, before anything is public, and attaches
	// the exported .deb files to the GitHub release only after the tag push.
	// The host is pinned to darwin: packages build inside docker, so a
	// release needs no Linux host with dpkg-deb.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	linuxDir := filepath.Join(setup.Cwd, "erun-devops", "linux")
	mustWriteFile(t, filepath.Join(linuxDir, "erun-cli", "Dockerfile"), untestedLinuxPackageDockerfile)
	mustWriteFile(t, filepath.Join(linuxDir, "erun-ui", "Dockerfile"), testedLinuxPackageDockerfile)
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux packages")

	envVars := append(releaseEnv(t, setup), "ERUN_HOST_OS_OVERRIDE=darwin")
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_build_release_exports_and_uploads_linux_packages", normalize.Apply(result.Combined))
}

func testReleaseDryRunLinuxPackageWithoutDistStageIsRefused(t *testing.T) {
	// A release refuses a package with no `dist` stage while resolving, so
	// it fails before any stamp, tag or build.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-ui", "Dockerfile"),
		"FROM golang:1.26.0 AS build\nRUN true\n\nFROM build AS test\nRUN true\n")
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux package")

	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected a package without a dist stage to be refused, got exit 0: %s", result.Combined)
	}
	golden.Equal(t, "release/dry_run_linux_package_without_dist_stage_is_refused", normalize.Apply(result.Combined))
}

func testReleaseDryRunMainWithMarketplaceEmitsShaSync(t *testing.T) {
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
}

func testReleaseDryRunMainWithScoopManifestValidatesAndSyncs(t *testing.T) {
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
}

func testReleaseDryRunMainWithAllPackagingArtifactsSyncsThem(t *testing.T) {
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
}

func testReleaseDryRunMainTracesAnonymousPullabilityProbeForTerraformReferencedImage(t *testing.T) {
	// A Terraform module under erun-devops/terraform-erun can reference a
	// released image directly (terraform-erun-cluster-edge's real
	// dns01_webhook_image is exactly this shape). verify-publication's
	// anonymous-pullability check must discover that reference and trace
	// probing it even in dry-run, where the real network probe is
	// skipped -- discovery and the per-image decision are not gated on
	// !DryRun, only the live registry call is.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	fixture.SeedTerraformModuleImageReference(t, setup.Cwd, "erun-webhook")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops")
	fixture.RunGit(t, setup.Cwd, "commit", "-m", "add terraform module image reference")
	// release marks source control only, so the publish and the preflights it
	// owns live in `build --release`, which is what this asserts.
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_main_traces_anonymous_pullability_probe_for_terraform_referenced_image", normalize.Apply(result.Combined))
}

func testReleaseDryRunMainWithInvalidScoopManifestFails(t *testing.T) {
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
}

func testReleaseDryRunMainWithEmptyScoopScriptFails(t *testing.T) {
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
}

func testReleaseDryRunMainWithMalformedScoopManifestFails(t *testing.T) {
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
}

func testReleaseDryRunReleaseTagAtHeadSkipsTagCreation(t *testing.T) {
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
}

func testReleaseDryRunStaleReleaseTagWithoutForceFails(t *testing.T) {
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
}

func testReleaseDryRunVersionFileAtProjectRoot(t *testing.T) {
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
}

func testReleaseDryRunMultipleReleaseRootsFails(t *testing.T) {
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
}

func testReleaseDryRunForceIncludesTagDeletionForStaleReleaseTag(t *testing.T) {
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
}

func testReleaseDryRunSkipsReleaseRootsInGitignoredTrees(t *testing.T) {
	// The release-root walker honors .gitignore (root + nested), so a
	// VERSION file in an ignored tree such as
	// `erun-docs/node_modules/lunr/VERSION` drops out of discovery instead
	// of yielding "multiple release roots found under project root".
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
}

func testReleaseDryRunWithUntrackedFileReportsWorktreeClean(t *testing.T) {
	// release publishes HEAD, not the worktree, so an untracked .idea/ (or
	// other unignored IDE/generator droppings) must not gate the flow. The
	// dry-run trace exposes the
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
}

func testReleaseDryRunInARuntimePodClaimsTheReleaseVersionLease(t *testing.T) {
	// Inside a runtime pod (ERUN_TENANT/ERUN_ENVIRONMENT injected by the
	// chart), release claims an exclusive, version-scoped activity lease
	// before doing anything else, so two orchestrators cannot race the same
	// release. No other release scenario sets these two
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
}

func testReleaseDryRunRefusesToReleaseAnImageItWouldNotPublish(t *testing.T) {
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
	// release marks source control only, so the publish and the preflights it
	// owns live in `build --release`, which is what this asserts.
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: componentCwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a release that cannot publish every image, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/dry_run_refuses_to_release_an_image_it_would_not_publish", normalize.Apply(result.Combined))
}

// releaseEnv declares the docker stub every release scenario needs: release
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
		`esac`,
		// A linux package's release export writes one .deb into its destination.
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    type=local,dest=*) dest="${arg#type=local,dest=}"; mkdir -p "$dest"; : > "$dest/package.deb" ;;`,
		`  esac`,
		`done`,
		`exit 0`,
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
	// The registry-credential preflight refuses up front when no
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

// remoteMainSubjects reads origin/main's commit subjects through a second
// checkout, so a scenario can assert what the release actually landed on the
// remote rather than what its own worktree believes.
func remoteMainSubjects(t *testing.T, repoDir string) string {
	t.Helper()
	fixture.RunGit(t, repoDir, "fetch", "-q", "origin")
	cmd := harnessexec.Command("git", "log", "--format=%s", "origin/main")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log origin/main: %v: %s", err, output)
	}
	return string(output)
}

func remoteTags(t *testing.T, setup env.Setup) string {
	t.Helper()
	cmd := harnessexec.Command("git", "ls-remote", "--tags", "origin")
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
