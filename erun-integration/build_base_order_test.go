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
	// Independent images build concurrently, and the ordering that allows it is
	// a pure function of the Dockerfiles — so it is auditable up front, and the
	// same on any machine. The degree is pinned here rather than resolved from
	// the host, or the assertion would depend on the runner's core count.
	t.Run("dry_run_reports_the_base_each_image_waits_for", testBuildDryRunReportsTheBaseEachImageWaitsFor)
	// --jobs 1 is the escape hatch back to sequential building, so it must not
	// merely be slower — it must produce what it always produced, including
	// keeping each image's decision lines beside its own build output.
	t.Run("dry_run_single_job_announces_no_ordering", testBuildDryRunSingleJobAnnouncesNoOrdering)
	// A FROM cycle is the one input a wave scheduler cannot make progress on, so
	// it has to be named rather than waited on forever.
	t.Run("a_from_cycle_fails_instead_of_hanging", testBuildAFromCycleFailsInsteadOfHanging)
	t.Run("dry_run_pinned_version_wrapper_resolves_local_base", testBuildDryRunPinnedVersionWrapperResolvesLocalBase)
	t.Run("real_run_versioned_wrapper_tags_per_arch_base", testBuildRealRunVersionedWrapperTagsPerArchBase)
}

func testBuildDryRunMissingBasePlatformCascadesDependentRebuild(t *testing.T) {
	// The docker stub is dry-run decision input (the fp-tag inspect answers
	// drive which branch the planner picks; without it the developer's local
	// image cache would shape the golden): the base image's arm64 fp-tag is
	// missing (exit 1) so base rebuilds, while every other fp-tag — including
	// both of api's — is present (exit 0). api FROMs the base tag, so despite
	// its own fingerprint hit it must cascade to a rebuild instead of promoting
	// a stale-base image. This pins the single-platform "missing for platform
	// linux/arm64" reason and the "rebuilding X because dependency Y is
	// rebuilding" cascade that the all-miss scenarios never reach.
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
	// Locks the fix for the ${ERUN_VERSION}-wrapper build failure. A wrapper
	// that FROMs its base via ${ERUN_VERSION} resolves the base's unsuffixed
	// local snapshot tag, which is never pushed and (tagged once per arch
	// under one name) only ever holds the last arch built. A multi-platform
	// wrapper build of the other arch therefore can't resolve it on a strict
	// image store. The fix: the base also publishes a per-arch stable tag
	// (…-snapshot-<arch>) and the wrapper's per-platform build asks for the
	// matching arch via ERUN_VERSION=<baseversion>-<arch>. The plan must show
	// both: the per-arch base tag and the per-arch ERUN_VERSION build-arg.
	// --environment local makes versions snapshot-suffixed so BaseVersion is
	// set; the stub answers every fp-tag inspect "missing" so all rebuild.
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
	// Version normalization collapses the arch suffix (…-snapshot-<ts>-amd64
	// and …-snapshot-amd64 both become <VERSION>), so the per-arch behavior
	// this scenario exists to lock is invisible in the golden. Assert it on
	// the raw output: the wrapper resolves its base per platform, and the
	// base publishes the matching per-arch stable tag. BaseVersion carries no
	// timestamp, so these are stable.
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
	// The wrapper FROMs api, so it is the one image that has to wait — and
	// the plan names api rather than a level the wrapper merely shares. The
	// version is left out of the assertion so it reads the same here as it
	// does in the golden, where the minted version is normalized.
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
	// `erun build --version <v>` is the pre-release gate: validate the release
	// build locally before any git ref moves. It used to be impossible for a
	// dependent image — a pinned-version local build tags only per-arch, so a
	// wrapper's FROM base:<v> resolved neither locally nor (unpublished) in the
	// registry and the build died "not found". The wrapper's per-platform build
	// must now ask for the base's per-arch local tag instead, without pushing
	// anything. Unlike the snapshot sibling above, the pinned version has no
	// -snapshot BaseVersion, so the arch suffix comes from the base being one of
	// this same build's own images.
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
	// Version normalization collapses 1.0.166 and 1.0.166-amd64 to the same
	// <VERSION> token, so the golden cannot tell the fixed plan from the broken
	// one. Assert the build args on the raw output: the wrapper resolves the
	// base per arch, while the base itself (no versioned FROM) keeps the plain
	// version so its own ERUN_VERSION still stamps the release.
	for _, want := range []string{
		"-t ghcr.io/sophium/wrapper:1.0.166-amd64 --build-arg ERUN_VERSION=1.0.166-amd64",
		"-t ghcr.io/sophium/wrapper:1.0.166-arm64 --build-arg ERUN_VERSION=1.0.166-arm64",
		"-t ghcr.io/sophium/api:1.0.166-amd64 --build-arg ERUN_VERSION=1.0.166 ",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Errorf("expected local base resolution %q in output:\n%s", want, result.Combined)
		}
	}
	// A local build must not mint the plain <version> tag: push's manifest
	// assembly owns that reference, and a local tag under the same name would
	// masquerade as the published multi-arch manifest.
	if strings.Contains(result.Combined, "docker tag ghcr.io/sophium/api:1.0.166-amd64 ghcr.io/sophium/api:1.0.166\n") {
		t.Errorf("local build must not tag the plain published version:\n%s", result.Combined)
	}
}

func testBuildRealRunVersionedWrapperTagsPerArchBase(t *testing.T) {
	// Real-run companion to the dry-run wrapper scenario. The docker stub
	// returns exit 1 for `image inspect` (no fp images → everything rebuilds)
	// and exit 0 otherwise, so the per-arch base re-tag and the wrapper build
	// run against the stub rather than a real daemon.
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
