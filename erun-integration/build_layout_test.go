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

func buildLayoutScenarios(t *testing.T) {
	t.Run("dry_run_from_devops_cwd", testBuildDryRunFromDevopsCwd)
	t.Run("dry_run_gitignored_project_config_warns", testBuildDryRunGitignoredProjectConfigWarns)
	t.Run("dry_run_cluster_registry_resolves_host_port_forward", testBuildDryRunClusterRegistryResolvesHostPortForward)
	t.Run("dry_run_configured_docker_and_version_paths", testBuildDryRunConfiguredDockerAndVersionPaths)
	t.Run("dry_run_component_auto_selects_lone_entry", testBuildDryRunComponentAutoSelectsLoneEntry)
	t.Run("dry_run_dockerfile_test_stage_grants_host_network_entitlement", testBuildDryRunDockerfileTestStageGrantsHostNetworkEntitlement)
	t.Run("dry_run_unconsumed_test_stage_is_refused", testBuildDryRunUnconsumedTestStageIsRefused)
	t.Run("dry_run_from_an_e2e_suite_directory_is_refused", testBuildDryRunFromAnE2eSuiteDirectoryIsRefused)
	t.Run("dry_run_component_flag_selects_entry", testBuildDryRunComponentFlagSelectsEntry)
	t.Run("component_selection_ambiguous_errors", testBuildComponentSelectionAmbiguousErrors)
	t.Run("component_flag_unknown_name_errors", testBuildComponentFlagUnknownNameErrors)
	t.Run("dry_run_paths_version_keeps_pinned_base", testBuildDryRunPathsVersionKeepsPinnedBase)
	t.Run("configured_docker_path_wrong_name_errors", testBuildConfiguredDockerPathWrongNameErrors)
	t.Run("dry_run_docker_context_repo_root", testBuildDryRunDockerContextRepoRoot)
	t.Run("dry_run_docker_context_component", testBuildDryRunDockerContextComponent)
	t.Run("configured_docker_context_invalid_errors", testBuildConfiguredDockerContextInvalidErrors)
	t.Run("dry_run_no_devops_recommends_build_env_skill", testBuildDryRunNoDevopsRecommendsBuildEnvSkill)
	t.Run("no_build_registry_errors", testBuildNoBuildRegistryErrors)
}

func testBuildDryRunFromDevopsCwd(t *testing.T) {
	setup := env.New(t)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "build/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
}

func testBuildDryRunGitignoredProjectConfigWarns(t *testing.T) {
	// A project config that resolves from disk but is excluded by .gitignore
	// silently works on this machine only, since git never sees it. The
	// build trace must surface that as a warning instead of staying silent.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedGitignoredProjectConfig(t, setup, "paths:\n    docker: build/docker\n    version: build/VERSION\n")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_gitignored_project_config_warns", normalize.Apply(result.Combined))
}

func testBuildDryRunClusterRegistryResolvesHostPortForward(t *testing.T) {
	// A cluster: container registry resolves its concrete push host from the
	// kube-context at build time. On the host (not in-pod) the push host is a
	// managed port-forward to the registry Service; dry-run traces the ClusterIP
	// lookup and the port-forward and uses placeholders so no cluster is touched.
	// Locks the cluster-registry build resolution (Concrete → expandClusterEntry
	// → resolver → port-forward) that plain registries skip.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "paths:\n    docker: build/docker\n    version: build/VERSION\ncontainerregistries:\n    - cluster: {}\n      roles:\n        - build\n        - deploy\n")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_cluster_registry_resolves_host_port_forward", normalize.Apply(result.Combined))
}

func testBuildDryRunConfiguredDockerAndVersionPaths(t *testing.T) {
	// paths.docker and paths.version in .erun/config.yaml relocate the docker
	// build root and VERSION file out of the <tenant>-devops convention: the
	// build discovers the component context under build/docker, mints the
	// version from build/VERSION, and traces both decisions. No -devops module
	// exists, yet the build-env advisory is suppressed because paths.docker is set.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "", "", "", "build/VERSION")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_configured_docker_and_version_paths", normalize.Apply(result.Combined))
}

func testBuildDryRunComponentAutoSelectsLoneEntry(t *testing.T) {
	// A single components: entry auto-selects with no --component flag,
	// exactly like today's implicit single-paths behavior. Each harness's
	// docker/version root lives outside the shared paths: block (the
	// monorepo-of-independent-deployables shape), so without selection this
	// harness's build would be unreachable at all.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectComponentsConfig(t, setup, fixture.ComponentPathsConfig{
		Name:    "platform-validator",
		Docker:  "harnesses/platform-validator/docker",
		Version: "harnesses/platform-validator/VERSION",
	})
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "docker"), "platform-validator")
	mustWriteFile(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_component_auto_selects_lone_entry", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerfileTestStageGrantsHostNetworkEntitlement(t *testing.T) {
	// BuildKit default-denies `RUN --network=host`, so a component test stage
	// that starts a container fixture cannot build at all unless erun grants
	// the network.host entitlement. The grant is scoped to a Dockerfile that
	// declares a `test` stage: `--allow network.host` hands a build step the
	// *builder's* network namespace — this environment pod's — which a
	// production image with no tests has no use for. This scenario is the
	// granted arm. The ungranted arm is every other build golden in this
	// suite, none of whose Dockerfiles declare a test stage: make the grant
	// unconditional again and all of them fail on the stray token, which is
	// the only reason this boundary is visible at all.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "", "", "", "build/VERSION")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "docker", "api", "Dockerfile"),
		"FROM --platform=$BUILDPLATFORM alpine:3.22 AS build\n"+
			"RUN mkdir /out\n"+
			"\n"+
			"FROM build AS test\n"+
			"RUN --network=host true\n"+
			"\n"+
			"FROM alpine:3.22 AS runtime\n"+
			"COPY --from=test /out /out\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_test_stage_grants_host_network_entitlement", normalize.Apply(result.Combined))
}

func testBuildDryRunUnconsumedTestStageIsRefused(t *testing.T) {
	// A `test` stage no later stage consumes would be pruned by BuildKit, so
	// the image would build without its tests ever running. The build spec
	// refuses it before anything is fingerprinted or built, naming the two
	// ways to consume it.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "", "", "", "build/VERSION")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "docker", "api", "Dockerfile"),
		"FROM --platform=$BUILDPLATFORM alpine:3.22 AS build\n"+
			"RUN mkdir /out\n"+
			"\n"+
			"FROM build AS test\n"+
			"RUN true\n"+
			"\n"+
			"FROM alpine:3.22 AS runtime\n"+
			"COPY --from=build /out /out\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected an unconsumed test stage to be refused, got exit 0: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_unconsumed_test_stage_is_refused", normalize.Apply(result.Combined))
}

func testBuildDryRunFromAnE2eSuiteDirectoryIsRefused(t *testing.T) {
	// An e2e suite's Dockerfile sits beside docker/ and k8s/ in the devops
	// module, so a build run from its directory would otherwise take it as
	// the current component and fingerprint, promote and push it. It is
	// refused with the command that does run it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	suiteDir := filepath.Join(setup.Cwd, "erun-devops", "e2e", "console")
	mustWriteFile(t, filepath.Join(suiteDir, "Dockerfile"),
		"FROM alpine:3.22 AS deps\n"+
			"\n"+
			"FROM deps AS e2e\n"+
			"RUN true\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: suiteDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected a build from an e2e suite directory to be refused, got exit 0: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_from_an_e2e_suite_directory_is_refused", normalize.Apply(result.Combined))
}

func testBuildDryRunComponentFlagSelectsEntry(t *testing.T) {
	// Two components: entries declared (two independent harnesses in one
	// monorepo); --component selects one by name, without editing the
	// committed config — two operators, two harnesses, no local edit.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectComponentsConfig(t, setup,
		fixture.ComponentPathsConfig{
			Name:    "platform-validator",
			Docker:  "harnesses/platform-validator/docker",
			Version: "harnesses/platform-validator/VERSION",
		},
		fixture.ComponentPathsConfig{
			Name:    "ingest-worker",
			Docker:  "harnesses/ingest-worker/docker",
			Version: "harnesses/ingest-worker/VERSION",
		},
	)
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "docker"), "platform-validator")
	mustWriteFile(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "VERSION"), "2.3.4\n")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "ingest-worker", "docker"), "ingest-worker")
	mustWriteFile(t, filepath.Join(setup.Cwd, "harnesses", "ingest-worker", "VERSION"), "9.9.9\n")
	result := erun.Run(t, []string{"build", "--component", "ingest-worker", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "platform-validator") {
		t.Errorf("expected only the selected ingest-worker harness in the plan, got:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_component_flag_selects_entry", normalize.Apply(result.Combined))
}

func testBuildComponentSelectionAmbiguousErrors(t *testing.T) {
	// More than one components: entry with no --component flag fails naming
	// the choices, rather than silently building whichever one convention
	// discovery happens to land on.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectComponentsConfig(t, setup,
		fixture.ComponentPathsConfig{Name: "ingest-worker", Docker: "harnesses/ingest-worker/docker"},
		fixture.ComponentPathsConfig{Name: "platform-validator", Docker: "harnesses/platform-validator/docker"},
	)
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "docker"), "platform-validator")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "ingest-worker", "docker"), "ingest-worker")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an ambiguous component selection, got 0:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "ingest-worker") || !strings.Contains(result.Combined, "platform-validator") {
		t.Errorf("expected the ambiguity error to name both declared components:\n%s", result.Combined)
	}
	golden.Equal(t, "build/component_selection_ambiguous_errors", normalize.Apply(result.Combined))
}

func testBuildComponentFlagUnknownNameErrors(t *testing.T) {
	// --component naming a component that .erun/config.yaml does not declare
	// fails loudly rather than silently falling back to convention discovery.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectComponentsConfig(t, setup, fixture.ComponentPathsConfig{
		Name:   "platform-validator",
		Docker: "harnesses/platform-validator/docker",
	})
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "harnesses", "platform-validator", "docker"), "platform-validator")
	result := erun.Run(t, []string{"build", "--component", "nope", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unknown --component name, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/component_flag_unknown_name_errors", normalize.Apply(result.Combined))
}

func testBuildDryRunPathsVersionKeepsPinnedBase(t *testing.T) {
	// A project-global paths.version is the project-level default and
	// must NOT clobber a component's own in-build-dir VERSION. The pinned base
	// keeps its 9.9.9 tag; a component without its own VERSION takes the
	// configured project version (snapshotted, since it is not build-dir-local).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "", "", "", "VERSION")
	mustWriteFile(t, filepath.Join(setup.Cwd, "VERSION"), "1.0.0\n")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "pinnedbase")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "docker", "pinnedbase", "VERSION"), "9.9.9\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Versions normalize to <VERSION> in the golden, so assert on the raw output
	// that the pinned base kept 9.9.9 and was not snapshotted by paths.version.
	if !strings.Contains(result.Combined, "ghcr.io/sophium/pinnedbase:9.9.9 ") {
		t.Errorf("expected pinned base image ghcr.io/sophium/pinnedbase:9.9.9 in output:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "pinnedbase:9.9.9-snapshot") {
		t.Errorf("pinned base was snapshotted despite its own VERSION file:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_paths_version_keeps_pinned_base", normalize.Apply(result.Combined))
}

func testBuildConfiguredDockerPathWrongNameErrors(t *testing.T) {
	// A paths.docker override pointing at a directory not named "docker" fails
	// with an error explaining the naming constraint, rather than silently
	// falling back to convention discovery.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "images", "", "", "", "")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "images"), "api")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a misnamed configured docker path, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/configured_docker_path_wrong_name_errors", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerContextRepoRoot(t *testing.T) {
	// paths.dockercontext: repo-root forces the docker build context to the
	// project root for a component nested deeper than the conventional
	// <devops>/docker/<component> layout (here docker is the 3rd path segment),
	// where the positional heuristic would otherwise pick the component dir.
	// This lets the Dockerfile COPY sibling repo paths. The context dir
	// normalizes to <TMP> in the golden, so the raw assert below proves the
	// resolved context is the project root.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	dockerDir := filepath.Join(setup.Cwd, "harnesses", "pv", "docker")
	fixture.SeedProjectPathsConfig(t, setup, "harnesses/pv/docker", "repo-root", "", "", "")
	fixture.SeedDockerComponentAt(t, dockerDir, "web")
	mustWriteFile(t, filepath.Join(setup.Cwd, "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The context dir is masked by <TMP> normalization; assert on the raw
	// output that docker build runs from the project root, not the component dir.
	if !strings.Contains(result.Combined, "cd "+setup.Cwd+" && docker build") {
		t.Errorf("expected docker build context at project root %q:\n%s", setup.Cwd, result.Combined)
	}
	if strings.Contains(result.Combined, "cd "+filepath.Join(dockerDir, "web")+" && docker build") {
		t.Errorf("docker build ran from the component dir despite paths.dockercontext: repo-root:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_docker_context_repo_root", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerContextComponent(t *testing.T) {
	// paths.dockercontext: component forces the docker build context to the
	// component dir even at the conventional <devops>/docker/<component> layout,
	// where the positional heuristic would otherwise pick the repo root. This is
	// the reverse override. The context dir normalizes to <TMP>, so the raw
	// assert below proves the resolved context is the component dir.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	dockerDir := filepath.Join(setup.Cwd, "build", "docker")
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "component", "", "", "build/VERSION")
	fixture.SeedDockerComponentAt(t, dockerDir, "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	componentDir := filepath.Join(dockerDir, "api")
	if !strings.Contains(result.Combined, "cd "+componentDir+" && docker build") {
		t.Errorf("expected docker build context at component dir %q:\n%s", componentDir, result.Combined)
	}
	if strings.Contains(result.Combined, "cd "+setup.Cwd+" && docker build") {
		t.Errorf("docker build ran from the project root despite paths.dockercontext: component:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_docker_context_component", normalize.Apply(result.Combined))
}

func testBuildConfiguredDockerContextInvalidErrors(t *testing.T) {
	// An unrecognized paths.dockercontext value fails the build loudly rather
	// than silently falling back to the positional heuristic.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "build/docker", "bogus", "", "", "build/VERSION")
	fixture.SeedDockerComponentAt(t, filepath.Join(setup.Cwd, "build", "docker"), "api")
	mustWriteFile(t, filepath.Join(setup.Cwd, "build", "VERSION"), "2.3.4\n")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an invalid docker context value, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/configured_docker_context_invalid_errors", normalize.Apply(result.Combined))
}

func testBuildDryRunNoDevopsRecommendsBuildEnvSkill(t *testing.T) {
	// A build in a project with no <tenant>-devops module emits the
	// erun-build-env skill advisory — even when the build itself succeeds via
	// a project build.sh, not only on failure.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", "build.sh")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
	result := erun.Run(t, []string{"build", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_no_devops_recommends_build_env_skill", normalize.Apply(result.Combined))
}

func testBuildNoBuildRegistryErrors(t *testing.T) {
	// A project registry list that marks no build registry cannot build:
	// `erun build` fails fast with the "no build registry" contract message
	// instead of silently falling back to the default registry. The list is
	// valid (from+to, deploy on to) — it simply omits the build role.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"containerregistries:\n"+
			"    - registry: ghcr.io/sophium\n"+
			"      roles: [from]\n"+
			"    - registry: registry.internal/team\n"+
			"      roles: [to, deploy]\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a project with no build registry, got 0: %s", result.Combined)
	}
	golden.Equal(t, "build/no_build_registry_errors", normalize.Apply(result.Combined))
}
