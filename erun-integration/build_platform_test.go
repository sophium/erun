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
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_from_release_repo_traces_docker_builds", normalize.Apply(result.Combined))
}

func testBuildDryRunReleaseIncludesReleaseAndBuildTraces(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_release_includes_release_and_build_traces", normalize.Apply(result.Combined))
}

func testBuildDryRunReleasePublishesRuntimeChart(t *testing.T) {
	// Image and chart are one contract, so the release publishes both.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	chartDir := filepath.Join(setup.Cwd, "erun-devops", "k8s", "erun-devops")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chartDir, err)
	}
	mustWriteFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: erun-devops\ndescription: ERun DevOps\nversion: 0.1.0\nappVersion: 0.1.0\n")
	// Push publishes the erun-devops chart in lockstep with its image.
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
	// The pinned base's chart publishes at the release version, not 9.9.9;
	// normalization hides both, so assert raw versions.
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
		// The trailing `*` is real buildx output and exercises marker stripping.
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
	// The stub reports only linux/amd64, so the preflight must fail fast on arm64.
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
	// "code1" has no entry and must inherit the project's platform pin.
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
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	result := erun.Run(t, []string{"build", "--release", "--dry-run", "--platform", "linux/amd64"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --release combined with --platform, got 0: %s", result.Combined)
	}
	golden.Equal(t, "build/release_platform_flag_conflict_errors", normalize.Apply(result.Combined))
}

func testBuildDryRunReleaseIgnoresConfiguredPlatforms(t *testing.T) {
	// A released artifact must deploy on any cluster.
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
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  buildx)`,
		`    case "$2" in inspect) echo "Platforms: linux/amd64, linux/arm64" ;; *) exit 0 ;; esac ;;`,
		// Marker-tracked so a first release is not reported as already published.
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
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	fixture.StubBinary(t, stubs, "helm", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "git", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"build", "--release", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_release_pushes_multi_platform_manifest", normalize.Apply(result.Combined))
}
