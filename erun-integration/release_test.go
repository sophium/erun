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
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_develop_emits_candidate_plan", normalize.Apply(result.Combined))
}

func testReleaseDryRunMainWithDevelopEmitsStablePlan(t *testing.T) {
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
	// `git checkout develop` creates a local branch from a remote-only ref, so checking
	// refs/heads alone would drop the develop stages.
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
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_main_without_develop_pushes_only_main", normalize.Apply(result.Combined))
}

func testReleaseDryRunBuildReleaseExportsAndUploadsLinuxPackages(t *testing.T) {
	// Packages build inside docker, so the darwin-pinned host needs no dpkg-deb; .debs attach
	// to the GitHub release only after the tag push.
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
	// The bump is gated on !DryRun, so the trace alone proves the wiring.
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
	// Checksum downloads are gated on !DryRun, so the trace alone locks the wiring.
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
	// Image references in terraform-erun modules must be discovered and traced even in
	// dry-run; only the live registry probe is skipped.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	fixture.SeedTerraformModuleImageReference(t, setup.Cwd, "erun-webhook")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops")
	fixture.RunGit(t, setup.Cwd, "commit", "-m", "add terraform module image reference")
	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/dry_run_main_traces_anonymous_pullability_probe_for_terraform_referenced_image", normalize.Apply(result.Combined))
}

func testReleaseDryRunMainWithInvalidScoopManifestFails(t *testing.T) {
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
	// assets/ dirs are excluded from release-root discovery.
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
	// The release-root walker honors .gitignore.
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
	// release publishes HEAD, so untracked IDE files must not gate it.
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
	// Only this scenario sets ERUN_TENANT/ERUN_ENVIRONMENT, so only its golden shows the
	// exclusive lease claim.
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
	// A component cwd would tag every image but build one, announcing unpublished
	// artifacts, so it is refused during resolution.
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
	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: componentCwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a release that cannot publish every image, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/dry_run_refuses_to_release_an_image_it_would_not_publish", normalize.Apply(result.Combined))
}

// releaseEnv stubs docker to report no local image, so plans consistently show a rebuild.
func releaseEnv(t *testing.T, setup env.Setup) []string {
	t.Helper()
	return append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
}

// `manifest inspect` reports published only after `manifest push` ran, so a first
// release is not mistaken for already published.
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
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    type=local,dest=*) dest="${arg#type=local,dest=}"; mkdir -p "$dest"; : > "$dest/package.deb" ;;`,
		`  esac`,
		`done`,
		`exit 0`,
	}, "\n"))
}

func stubPublishToolchain(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDockerWithManifestTracking(t, stubs)
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := fixture.StubEnv(stubs, "docker", "helm")
	// GH_TOKEN stands in for the ghcr.io credential a real toolchain has.
	return append(envVars, "GH_TOKEN=integration-test-token")
}

// seedBareOrigin gives a real remote so "the tag is public" is observable.
func seedBareOrigin(t *testing.T, setup env.Setup) string {
	t.Helper()
	remoteRoot := filepath.Join(setup.Home, "origin.git")
	fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
	fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
	fixture.RunGit(t, setup.Cwd, "push", "-u", "-q", "origin", "main")
	return remoteRoot
}

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
