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
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildPlatformScenarios(t *testing.T) {
	t.Run("dry_run_from_release_repo_traces_docker_builds", testBuildDryRunFromReleaseRepoTracesDockerBuilds)
	t.Run("dry_run_release_includes_release_and_build_traces", testBuildDryRunReleaseIncludesReleaseAndBuildTraces)
	t.Run("dry_run_release_publishes_runtime_chart", testBuildDryRunReleasePublishesRuntimeChart)
	t.Run("dry_run_configured_fingerprint_traces_pull_and_tag", testBuildDryRunConfiguredFingerprintTracesPullAndTag)
	t.Run("real_run_via_docker_stub_drives_multi_platform_build", testBuildRealRunViaDockerStubDrivesMultiPlatformBuild)
	t.Run("real_run_fails_when_daemon_cannot_build_required_platform", testBuildRealRunFailsWhenDaemonCannotBuildRequiredPlatform)
	t.Run("dry_run_platform_flag_narrows_build_to_one_architecture", testBuildDryRunPlatformFlagNarrowsBuildToOneArchitecture)
	t.Run("dry_run_configured_platforms_narrows_build_to_one_architecture", testBuildDryRunConfiguredPlatformsNarrowsBuildToOneArchitecture)
	t.Run("dry_run_project_platform_default_narrows_unlisted_environment", testBuildDryRunProjectPlatformDefaultNarrowsUnlistedEnvironment)
	t.Run("dry_run_empty_platform_list_opts_environment_out_of_project_default", testBuildDryRunEmptyPlatformListOptsEnvironmentOutOfProjectDefault)
	t.Run("release_platform_flag_conflict_errors", testBuildReleasePlatformFlagConflictErrors)
	t.Run("dry_run_release_ignores_configured_platforms", testBuildDryRunReleaseIgnoresConfiguredPlatforms)
	t.Run("real_run_release_pushes_multi_platform_manifest", testBuildRealRunReleasePushesMultiPlatformManifest)
}

func testBuildDryRunFromReleaseRepoTracesDockerBuilds(t *testing.T) {
	// --dry-run traces one docker build per discovered Dockerfile from a
	// release-shape project root.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_from_release_repo_traces_docker_builds", normalize.Apply(result.Combined))
}

func testBuildDryRunReleaseIncludesReleaseAndBuildTraces(t *testing.T) {
	// --release dry-run combines the release plan (sync, version write, tag)
	// with the docker build trace using the release-resolved version.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_release_includes_release_and_build_traces", normalize.Apply(result.Combined))
}

func testBuildDryRunReleasePublishesRuntimeChart(t *testing.T) {
	// A release build publishes each component's chart alongside its image and
	// verifies it is fetchable — image and chart are one contract. The release
	// root carries both the erun-devops chart and the seeded api component, so
	// the golden shows both publishing, not just the runtime.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	chartDir := filepath.Join(setup.Cwd, "erun-devops", "k8s", "erun-devops")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chartDir, err)
	}
	mustWriteFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: erun-devops\ndescription: ERun DevOps\nversion: 0.1.0\nappVersion: 0.1.0\n")
	// A real release also builds and pushes the erun-devops image, and push
	// publishes its chart in lockstep — so seed that build context too.
	runtimeDockerDir := filepath.Join(setup.Cwd, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(runtimeDockerDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", runtimeDockerDir, err)
	}
	mustWriteFile(t, filepath.Join(runtimeDockerDir, "Dockerfile"), "FROM alpine:3.22\n")
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add runtime chart and image")
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_release_publishes_runtime_chart", normalize.Apply(result.Combined))
	// The version-pinned base (docker/base/VERSION=9.9.9) keeps its
	// image at the upstream pin, but its co-located chart must publish at the
	// release version — the same version the built `api` chart publishes at,
	// NOT 9.9.9. Output normalization collapses both 1.4.2-pr.<sha> and 9.9.9
	// to <VERSION>, so the golden can't tell them apart; assert the raw
	// versions directly.
	baseChartVer := chartPackageVersion(t, result.Combined, "base")
	apiChartVer := chartPackageVersion(t, result.Combined, "api")
	if baseChartVer != apiChartVer {
		t.Fatalf("base chart published at %q, want the release version %q (same as api)", baseChartVer, apiChartVer)
	}
	if strings.Contains(baseChartVer, "9.9.9") {
		t.Fatalf("base chart published at the pinned image version %q; want the release version", baseChartVer)
	}
}

func testBuildDryRunConfiguredFingerprintTracesPullAndTag(t *testing.T) {
	// When an image name matches a configured docker.fingerprints entry, the
	// materialize step traces docker manifest inspect / pull / tag per platform
	// before the regular fingerprint inspect. Dry-run only traces the would-be
	// pull, never performs it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      fingerprints:\n"+
			"        base: 0123456789abcdef\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_configured_fingerprint_traces_pull_and_tag", normalize.Apply(result.Combined))
}

func testBuildRealRunViaDockerStubDrivesMultiPlatformBuild(t *testing.T) {
	// Real-run: the docker stub exits 0 for every invocation so the
	// multi-platform build runs to completion across both platforms without a
	// real daemon. Asserts the user-facing "Built" / "Tagged" lines appear.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in`,
		`      inspect) exit 1 ;;`,
		`      *) exit 0 ;;`,
		`    esac`,
		`    ;;`,
		// The multi-arch daemon-capability preflight runs
		// `docker buildx inspect` before building; report both required
		// platforms so it passes. The trailing `*` on the node default is
		// realistic buildx output and exercises the marker-stripping parse.
		`  buildx)`,
		`    case "$2" in`,
		`      inspect) echo "Platforms: linux/arm64*, linux/amd64" ;;`,
		`      *) exit 0 ;;`,
		`    esac`,
		`    ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_via_docker_stub_drives_multi_platform_build", normalize.Apply(result.Combined))
}

func testBuildRealRunFailsWhenDaemonCannotBuildRequiredPlatform(t *testing.T) {
	// The multi-arch daemon-capability preflight: by default this build
	// targets linux/amd64 + linux/arm64, so before shelling `docker build`
	// per platform it runs `docker buildx inspect` and fails fast with a
	// direct, actionable error when the daemon has no emulator for a
	// required platform — instead of the opaque per-platform `docker build`
	// failure. The stub reports only linux/amd64, so linux/arm64 is
	// unbuildable regardless of host arch. Real-run, not dry-run, because the
	// preflight guards the real executor.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx)`,
		`    case "$2" in inspect) echo "Platforms: linux/amd64" ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected build to fail when the daemon cannot build a required platform; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/real_run_fails_when_daemon_cannot_build_required_platform", normalize.Apply(result.Combined))
}

func testBuildDryRunPlatformFlagNarrowsBuildToOneArchitecture(t *testing.T) {
	// --platform overrides the default multi-arch build to only the named
	// platform(s), so an environment whose cluster can only ever run one
	// architecture stops paying for an emulated build of the other.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--dry-run", "--platform", "linux/amd64"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected --platform linux/amd64 to exclude arm64 from the build plan:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_platform_flag_narrows_build_to_one_architecture", normalize.Apply(result.Combined))
}

func testBuildDryRunConfiguredPlatformsNarrowsBuildToOneArchitecture(t *testing.T) {
	// environments.<env>.docker.platforms in .erun/config.yaml pins a build
	// to one architecture permanently, without a --platform flag on every
	// invocation — for an environment whose cluster can only ever run it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      platforms: [linux/amd64]\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected configured docker.platforms to exclude arm64 from the build plan:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_configured_platforms_narrows_build_to_one_architecture", normalize.Apply(result.Combined))
}

func testBuildDryRunProjectPlatformDefaultNarrowsUnlistedEnvironment(t *testing.T) {
	// docker.platforms at the top level of .erun/config.yaml is the project
	// default, inherited by every environment that declares no platforms of
	// its own. A project whose machines are all single-architecture states
	// the pin once, so an environment nobody listed (here "code1", which has
	// no entry at all) cannot silently fall back to the multi-arch build and
	// pay for an emulated architecture its node cannot run.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"docker:\n"+
			"  platforms: [linux/amd64]\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "code1"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected the project-wide docker.platforms default to exclude arm64 from the build plan:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_project_platform_default_narrows_unlisted_environment", normalize.Apply(result.Combined))
}

func testBuildDryRunEmptyPlatformListOptsEnvironmentOutOfProjectDefault(t *testing.T) {
	// The explicit opt-out: an environment declaring platforms: [] is not
	// pinned by the project default, so the generic environment name `erun
	// init` assigns keeps building every platform a contributor's own
	// machine may need.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"docker:\n"+
			"  platforms: [linux/amd64]\n"+
			"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      platforms: []\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected an explicit platforms: [] to restore the multi-arch build:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_empty_platform_list_opts_environment_out_of_project_default", normalize.Apply(result.Combined))
}

func testBuildReleasePlatformFlagConflictErrors(t *testing.T) {
	// --release always publishes every platform erun supports, so combining
	// it with an explicit --platform override is refused rather than
	// silently narrowing a released artifact.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--release", "--dry-run", "--platform", "linux/amd64"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --release combined with --platform, got 0: %s", result.Combined)
	}
	golden.Equal(t, "build/release_platform_flag_conflict_errors", normalize.Apply(result.Combined))
}

func testBuildDryRunReleaseIgnoresConfiguredPlatforms(t *testing.T) {
	// A release build must publish every platform erun supports
	// regardless of the environment's configured docker.platforms pin — a
	// released artifact has to be deployable on any cluster, not just the
	// one that happened to build it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      platforms: [linux/amd64]\n",
	)
	result := erun.Run(t, []string{"build", "--release", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "linux/amd64") || !strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected a release build to still target both platforms despite the configured single-arch pin:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_release_ignores_configured_platforms", normalize.Apply(result.Combined))
}

func testBuildRealRunReleasePushesMultiPlatformManifest(t *testing.T) {
	// Real-run release mode pushes the multi-platform manifest: release-tagged
	// images run the per-platform push and the manifest create+push.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		// Multi-arch capability preflight: report both required
		// platforms so `docker buildx inspect` passes.
		`  buildx)`,
		`    case "$2" in inspect) echo "Platforms: linux/amd64, linux/arm64" ;; *) exit 0 ;; esac ;;`,
		// manifest inspect backs both the pre-publish probe and the
		// post-publish verify; marker-file-tracked so this scenario (a
		// first-ever release, per "fingerprint image not found locally"
		// below) does not falsely report the image as already published
		// before manifest push has run.
		`  manifest)`,
		`    marker="` + stubs + `/manifest-published-$(printf '%s' "$3" | tr '/:' '__')"`,
		`    case "$2" in`,
		`      inspect) [ -f "$marker" ] && exit 0 || exit 1 ;;`,
		`      push) touch "$marker" ; exit 0 ;;`,
		`      *) exit 0 ;;`,
		`    esac`,
		`    ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// Release flow runs git tag/push; stub git verb-by-verb so the
	// release stage succeeds without touching a real remote.
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	// The repo already exists from the seed's real git; the stub covers only
	// erun's own release git operations (tag/push).
	fixture.StubBinary(t, stubs, "helm", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "git", "helm")...)
	// Give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the manifest-push behavior it is about.
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"build", "--release", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_release_pushes_multi_platform_manifest", normalize.Apply(result.Combined))
}
