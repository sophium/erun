package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildBaseOrderingScenarios(t *testing.T) {
	t.Run("dry_run_missing_base_platform_cascades_dependent_rebuild", testBuildDryRunMissingBasePlatformCascadesDependentRebuild)
	t.Run("dry_run_versioned_wrapper_resolves_per_arch_base", testBuildDryRunVersionedWrapperResolvesPerArchBase)
	// The degree is pinned, not resolved from the host's core count.
	t.Run("dry_run_reports_the_base_each_image_waits_for", testBuildDryRunReportsTheBaseEachImageWaitsFor)
	// --jobs 1 must produce exactly the sequential output.
	t.Run("dry_run_single_job_announces_no_ordering", testBuildDryRunSingleJobAnnouncesNoOrdering)
	t.Run("a_from_cycle_fails_instead_of_hanging", testBuildAFromCycleFailsInsteadOfHanging)
	t.Run("dry_run_pinned_version_wrapper_resolves_local_base", testBuildDryRunPinnedVersionWrapperResolvesLocalBase)
	t.Run("real_run_versioned_wrapper_tags_per_arch_base", testBuildRealRunVersionedWrapperTagsPerArchBase)
}

func testBuildDryRunMissingBasePlatformCascadesDependentRebuild(t *testing.T) {
	// The stub reports base's arm64 fp-tag missing and every other present, so
	// api must cascade to a rebuild despite its own fingerprint hit.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"),
		"FROM ghcr.io/sophium/base:9.9.9\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/api/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "api depends on local base image")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$*" in`,
		`  "image inspect ghcr.io/sophium/base:fp-"*"-arm64") exit 1 ;;`,
		`  "image inspect"*) exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_missing_base_platform_cascades_dependent_rebuild", normalize.Apply(result.Combined))
}

func testBuildDryRunVersionedWrapperResolvesPerArchBase(t *testing.T) {
	// A ${ERUN_VERSION} wrapper must resolve the base's per-arch tag
	// (…-snapshot-<arch>): the unsuffixed local tag holds only the last arch built.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
		"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_versioned_wrapper_resolves_per_arch_base", normalize.Apply(result.Combined))
	// Normalization hides the arch suffix, so assert on the raw output.
	for _, want := range []string{
		"--build-arg ERUN_VERSION=1.4.2-snapshot-amd64",
		"--build-arg ERUN_VERSION=1.4.2-snapshot-arm64",
		"ghcr.io/sophium/api:1.4.2-snapshot-amd64",
		"ghcr.io/sophium/api:1.4.2-snapshot-arm64",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Errorf("expected per-arch base resolution %q in output:\n%s", want, result.Combined)
		}
	}
}

func testBuildDryRunReportsTheBaseEachImageWaitsFor(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
		"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
	result := erun.Run(t, []string{"build", "--jobs", "2", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The version is omitted so this matches the normalized golden too.
	plan := ""
	for _, line := range strings.Split(result.Combined, "\n") {
		if strings.HasPrefix(line, "build: ") {
			plan = line
		}
	}
	if !strings.Contains(plan, "3 images, 1 waiting on a sibling base — ghcr.io/sophium/wrapper:") ||
		!strings.Contains(plan, " after ghcr.io/sophium/api:") {
		t.Fatalf("expected the wrapper to be the one image named as waiting on api, got %q:\n%s", plan, result.Combined)
	}
}

func testBuildDryRunSingleJobAnnouncesNoOrdering(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--jobs", "1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "waiting on a sibling base") {
		t.Fatalf("a sequential build has no ordering to announce:\n%s", result.Combined)
	}
}

func testBuildAFromCycleFailsInsteadOfHanging(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"),
		"FROM ghcr.io/sophium/base:${ERUN_VERSION}\nCMD [\"true\"]\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "base", "Dockerfile"),
		"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "make api and base FROM each other")
	result := erun.Run(t, []string{"build", "--jobs", "2", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected a cycle to fail the build:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "cycle") {
		t.Fatalf("the failure must name the cycle:\n%s", result.Combined)
	}
}

func testBuildDryRunPinnedVersionWrapperResolvesLocalBase(t *testing.T) {
	// A pinned-version local build tags only per-arch, so the wrapper must ask
	// for the base's per-arch tag, without pushing.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
		"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
	result := erun.Run(t, []string{"build", "--version", "1.0.166", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_pinned_version_wrapper_resolves_local_base", normalize.Apply(result.Combined))
	// Normalization hides the arch suffix, so assert on the raw output.
	for _, want := range []string{
		"-t ghcr.io/sophium/wrapper:1.0.166-amd64 --build-arg ERUN_VERSION=1.0.166-amd64",
		"-t ghcr.io/sophium/wrapper:1.0.166-arm64 --build-arg ERUN_VERSION=1.0.166-arm64",
		"-t ghcr.io/sophium/api:1.0.166-amd64 --build-arg ERUN_VERSION=1.0.166 ",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Errorf("expected local base resolution %q in output:\n%s", want, result.Combined)
		}
	}
	// The plain tag belongs to push's manifest assembly.
	if strings.Contains(result.Combined, "docker tag ghcr.io/sophium/api:1.0.166-amd64 ghcr.io/sophium/api:1.0.166\n") {
		t.Errorf("local build must not tag the plain published version:\n%s", result.Combined)
	}
}

func testBuildRealRunVersionedWrapperTagsPerArchBase(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
		"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1 $2" in`,
		`  "image inspect") exit 1 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_versioned_wrapper_tags_per_arch_base", normalize.Apply(result.Combined))
}
