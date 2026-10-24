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
	// The root `erun push` shorthand only registers when a Dockerfile is
	// present in cwd; without the seed, `push --help` falls through to root
	// help and the push-specific flags never appear in the golden.
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
	// push is a pure primitive: it publishes the version `erun build`
	// minted and never mints one, so a bare `erun push` with no version
	// must fail fast (root AGENTS.md § "Command primitives vs
	// orchestration"). The gate fires before any docker call, so no stub
	// is needed.
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
	// Even in dry-run, push's incremental fingerprint check inspects the
	// local image store (`docker image inspect`) to decide
	// promote-vs-rebuild — a sanctioned dry-run decision input
	// (erun-integration/AGENTS.md § "stubs as dry-run decision input").
	// Stub docker for determinism and so no real daemon is needed (the
	// erun-devops image test stage runs this gate with no docker on PATH);
	// `inspect` exits 1 so nothing is cached and the plan traces rebuild.
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
	// `docker manifest` never consults the daemon's --insecure-registry
	// list — it talks to the registry directly and defaults to HTTPS — so a
	// BUILD-role cluster registry marked `insecure: true` must still see
	// `--insecure` on every manifest subcommand that hits the network
	// (create/push), or assembly fails "no such manifest" against a plain
	// HTTP in-cluster registry even though both per-arch pushes succeeded.
	// `manifest rm` never leaves the local store, so it must stay bare.
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
	// environments.<env>.docker.platforms in .erun/config.yaml narrows
	// what a local build mints and never what a push publishes: push
	// resolves every platform erun supports, the same override a release
	// build applies, so a project whose machines all build amd64 alone
	// still publishes a manifest that carries arm64. The pin reaching the
	// registry is what shipped an amd64-only release manifest and left
	// every aarch64 environment with no image to pull.
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
	// Stated separately from the snapshot so a blanket golden regeneration
	// cannot quietly narrow a published version back to one architecture.
	if !strings.Contains(result.Combined, "linux/arm64") {
		t.Fatalf("expected configured docker.platforms to leave arm64 in the push plan:\n%s", result.Combined)
	}
	golden.Equal(t, "push/dry_run_configured_platforms_do_not_narrow_push", normalize.Apply(result.Combined))
}

func testPushDryRunBuildShortcutBuildsThenPushesMintedVersion(t *testing.T) {
	// `erun push --build` is the operator shortcut that builds the current
	// source first (minting a snapshot version) and pushes that exact
	// version, no --version needed.
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
	// --force must propagate to the --build step so the build rebuilds
	// every image instead of promoting from the fingerprint cache.
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
	// --build mints the version itself, so combining it with an
	// explicit --version is contradictory and must fail clearly before any
	// build or push work.
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
	// The docker stub reports no cached fingerprint images (image inspect
	// exit 1) so the real build+push path runs instead of promoting.
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
	// A locally-built multi-arch image only exists under per-arch tags, so
	// a multi-image push must push those and assemble a manifest list, not
	// `docker push` the arch-less version tag (which would fail "tag does
	// not exist"). The stub answers every fingerprint inspect as present so
	// images promote, mirroring the real failure this locks.
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
	// Companion real-run to the dry-run manifest scenario: the per-arch
	// manifest assembly must complete without an arch-less push.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
	fixture.StubBinary(t, stubs, "helm", "")
	// The registry-credential preflight needs a resolvable credential
	// to let this scenario reach the manifest-assembly behavior it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
}

func testPushRealRunTransientChartVerifyReadRetriesThenVerifies(t *testing.T) {
	// A release aborted mid-publish because the read-back of a chart erun had
	// just pushed answered 403 denied — the registry minted the pull token
	// before the new tag propagated. Verification reads an object erun itself
	// wrote, so it must retry with bounded backoff instead of failing the
	// publish. The helm stub fails the first `pull` with the exact ghcr error
	// and succeeds after, so the whole push must still complete.
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
	// Give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the chart-verify-read retry it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_transient_chart_verify_read_retries_then_verifies", normalize.Apply(result.Combined))
	// The pull count is a side effect outside the captured streams: 3 proves
	// the failed read really was retried (api fail + retry, then base) rather
	// than the failure being swallowed.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read helm pull counter: %v", err)
	}
	if pulls, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pulls != 3 {
		t.Fatalf("expected 3 helm pull invocations (fail + retry + second chart), got %q", rawCount)
	}
}

func testPushRealRunPersistentChartVerifyReadReportsPartialPublish(t *testing.T) {
	// The other half of the retry contract: a read that never succeeds must
	// still fail the push, and because the version's images are already public
	// by then, the failure must name exactly which charts landed, which did
	// not, and that re-running push is the recovery. The extra `zeta` chart
	// makes the middle chart fail so all three lists are non-empty.
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
	// Give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the partial-publish report it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a chart that never verifies, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_persistent_chart_verify_read_reports_partial_publish", normalize.Apply(result.Combined))
}
