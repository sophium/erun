package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestPush(t *testing.T) {
	t.Parallel()
	t.Run("help", testPushHelp)
	t.Run("missing_version_errors", testPushMissingVersionErrors)
	pushRegistryAuthScenarios(t)
	t.Run("devops_container_push_real_run_resolves_single_image_spec", testPushDevopsContainerPushRealRunResolvesSingleImageSpec)
	t.Run("dry_run_single_image_from_dockerfile_cwd", testPushDryRunSingleImageFromDockerfileCwd)
	t.Run("dry_run_insecure_cluster_registry_passes_insecure_to_manifest_commands", testPushDryRunInsecureClusterRegistryPassesInsecureToManifestCommands)
	t.Run("dry_run_configured_platforms_do_not_narrow_push", testPushDryRunConfiguredPlatformsDoNotNarrowPush)
	t.Run("dry_run_build_shortcut_builds_then_pushes_minted_version", testPushDryRunBuildShortcutBuildsThenPushesMintedVersion)
	t.Run("dry_run_build_shortcut_force_rebuilds", testPushDryRunBuildShortcutForceRebuilds)
	t.Run("build_shortcut_with_explicit_version_errors", testPushBuildShortcutWithExplicitVersionErrors)
	t.Run("real_run_single_image_from_dockerfile_cwd", testPushRealRunSingleImageFromDockerfileCwd)
	t.Run("dry_run_local_push_assembles_per_arch_manifest", testPushDryRunLocalPushAssemblesPerArchManifest)
	t.Run("real_run_local_push_assembles_per_arch_manifest", testPushRealRunLocalPushAssemblesPerArchManifest)
	t.Run("real_run_transient_chart_verify_read_retries_then_verifies", testPushRealRunTransientChartVerifyReadRetriesThenVerifies)
	t.Run("real_run_persistent_chart_verify_read_reports_partial_publish", testPushRealRunPersistentChartVerifyReadReportsPartialPublish)
	pushStepTimingScenarios(t)
}

func testPushHelp(t *testing.T) {
	// The root `erun push` shorthand registers only when cwd has a Dockerfile.
	setup := env.New(t)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	result := erun.Run(t, []string{"push", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/help", normalize.Apply(result.Combined))
}

func testPushMissingVersionErrors(t *testing.T) {
	// push never mints a version, so a bare push fails before any docker call.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	result := erun.Run(t, []string{"push", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for push without a version, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/missing_version_errors", normalize.Apply(result.Combined))
}

func testPushDevopsContainerPushRealRunResolvesSingleImageSpec(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"devops", "container", "push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/devops_container_push_real_run_resolves_single_image_spec", normalize.Apply(result.Combined))
}

func testPushDryRunSingleImageFromDockerfileCwd(t *testing.T) {
	// Dry-run still inspects the local image store to choose promote vs rebuild; the stub
	// exits 1 so the plan traces a rebuild without a daemon.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/dry_run_single_image_from_dockerfile_cwd", normalize.Apply(result.Combined))
}

func testPushDryRunInsecureClusterRegistryPassesInsecureToManifestCommands(t *testing.T) {
	// `docker manifest` ignores the daemon's --insecure-registry list, so create/push need
	// --insecure for an insecure registry; `manifest rm` is local and stays bare.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	fixture.SeedProjectK8sConfig(t, setup, "containerregistries:\n    - cluster: {insecure: true}\n      roles:\n        - build\n        - deploy\n")
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/dry_run_insecure_cluster_registry_passes_insecure_to_manifest_commands", normalize.Apply(result.Combined))
}

func testPushDryRunConfiguredPlatformsDoNotNarrowPush(t *testing.T) {
	// docker.platforms narrows local builds only; push publishes every supported platform
	// so the release manifest keeps arm64.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  dev:\n"+
			"    docker:\n"+
			"      platforms: [linux/amd64]\n",
	)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run", "--environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Separate from the golden so a blanket regeneration cannot drop arm64.
	if !strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected configured docker.platforms to leave arm64 in the push plan:\n%s", result.Combined)
	}
	golden.Equal(t, "push/dry_run_configured_platforms_do_not_narrow_push", normalize.Apply(result.Combined))
}

func testPushDryRunBuildShortcutBuildsThenPushesMintedVersion(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/dry_run_build_shortcut_builds_then_pushes_minted_version", normalize.Apply(result.Combined))
}

func testPushDryRunBuildShortcutForceRebuilds(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--build", "--force", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/dry_run_build_shortcut_force_rebuilds", normalize.Apply(result.Combined))
}

func testPushBuildShortcutWithExplicitVersionErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	result := erun.Run(t, []string{"push", "--build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --build with --version, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/build_shortcut_with_explicit_version_errors", normalize.Apply(result.Combined))
}

func testPushRealRunSingleImageFromDockerfileCwd(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectDockerfile(t, setup)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_single_image_from_dockerfile_cwd", normalize.Apply(result.Combined))
}

func testPushDryRunLocalPushAssemblesPerArchManifest(t *testing.T) {
	// Multi-arch images exist only under per-arch tags, so push assembles a manifest list
	// instead of pushing the arch-less tag.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/dry_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
}

func testPushRealRunLocalPushAssemblesPerArchManifest(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
}

func testPushRealRunTransientChartVerifyReadRetriesThenVerifies(t *testing.T) {
	// The registry can answer 403 on a read-back of a just-pushed chart before the tag
	// propagates, so verification retries with backoff.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	counter := setup.Cwd + "/helm-pull-counter"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`case "$1" in`,
		`  pull)`,
		`    count=0`,
		`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`    count=$((count + 1))`,
		`    printf '%s' "$count" > '` + counter + `'`,
		`    if [ "$count" = "1" ]; then`,
		`      printf 'Error: failed to perform "FetchReference" on source: response status code 403: denied: denied\n' >&2`,
		`      exit 1`,
		`    fi`,
		`    exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_transient_chart_verify_read_retries_then_verifies", normalize.Apply(result.Combined))
	// 3 pulls prove the failed read was retried, not swallowed.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read helm pull counter: %v", err)
	}
	if pulls, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pulls != 3 {
		t.Fatalf("expected 3 helm pull invocations (fail + retry + second chart), got %q", rawCount)
	}
}

func testPushRealRunPersistentChartVerifyReadReportsPartialPublish(t *testing.T) {
	// A read that never succeeds fails the push, naming which charts landed and which did not;
	// `zeta` makes the middle chart fail so all lists are non-empty.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "k8s", "zeta", "Chart.yaml"),
		"apiVersion: v2\nname: zeta\nversion: 0.1.0\nappVersion: 0.1.0\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`case "$1" in`,
		`  pull)`,
		`    case "$2" in`,
		`      */charts/base)`,
		`        printf 'Error: failed to perform "FetchReference" on source: response status code 403: denied: denied\n' >&2`,
		`        exit 1 ;;`,
		`    esac`,
		`    exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a chart that never verifies, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_persistent_chart_verify_read_reports_partial_publish", normalize.Apply(result.Combined))
}
