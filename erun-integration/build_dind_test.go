package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildDindScenarios(t *testing.T) {
	t.Run("dry_run_dockerfile_dind_args_resolve_configured_runtime_dind_pod", testBuildDryRunDockerfileDindArgsResolveConfiguredRuntimeDindPod)
	t.Run("dry_run_dockerfile_dind_args_default_to_conservative_constant_when_unconfigured", testBuildDryRunDockerfileDindArgsDefaultToConservativeConstantWhenUnconfigured)
	t.Run("dry_run_dockerfile_without_dind_args_is_unaffected", testBuildDryRunDockerfileWithoutDindArgsIsUnaffected)
	t.Run("dry_run_dockerfile_cgroup_parent_applies_inside_injected_runtime_pod", testBuildDryRunDockerfileCgroupParentAppliesInsideInjectedRuntimePod)
	t.Run("dry_run_dockerfile_cgroup_parent_absent_outside_injected_runtime_pod", testBuildDryRunDockerfileCgroupParentAbsentOutsideInjectedRuntimePod)
}

func testBuildDryRunDockerfileDindArgsResolveConfiguredRuntimeDindPod(t *testing.T) {
	// A Dockerfile that declares ARG DIND_CPU_LIMIT / ARG
	// DIND_MEMORY_LIMIT_MIB (the erun-devops Dockerfile's own in-build gate
	// sizing) must have those ARGs fed from the *building* environment's
	// actual configured erun-dind sidecar resources
	// (EnvConfig.RuntimeDindPod), not the Dockerfile's own hardcoded
	// defaults and never the host's raw node capacity. team/dev's config
	// sets a non-default runtimedindpod (6 CPU / 24Gi) so the golden can't
	// be satisfied by accident from the 4-core/20Gi fallback default.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	appendEnvConfigForTest(t, setup, "team", "dev", "runtimedindpod:\n  cpu: \"6\"\n  memory: 24Gi\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "VERSION"), "1.0.0\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops", "Dockerfile"),
		"FROM alpine:3.22\nARG DIND_CPU_LIMIT=4\nARG DIND_MEMORY_LIMIT_MIB=20480\n")
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "--build-arg DIND_CPU_LIMIT=6 --build-arg DIND_MEMORY_LIMIT_MIB=24576") {
		t.Errorf("expected the docker build to carry the configured runtimedindpod values:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_dind_args_resolve_configured_runtime_dind_pod", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerfileDindArgsDefaultToConservativeConstantWhenUnconfigured(t *testing.T) {
	// An environment with no configured runtimedindpod must not fall back
	// to the host node's real CPU/memory capacity -- it must fall back to the same fixed constant the
	// sidecar's own chart default and the Dockerfile's own ARG default
	// use, regardless of how large the machine actually running this test
	// is. The constant's own value is erun-common's rule to decide (it is
	// sized for a node, not for the host this happens to run on), so it is
	// read from there rather than restated here.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "VERSION"), "1.0.0\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops", "Dockerfile"),
		"FROM alpine:3.22\nARG DIND_CPU_LIMIT=4\nARG DIND_MEMORY_LIMIT_MIB=20480\n")
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	want := "--build-arg DIND_CPU_LIMIT=" + config.DefaultRuntimeDindCPU + " --build-arg DIND_MEMORY_LIMIT_MIB=20480"
	if !strings.Contains(result.Combined, want) {
		t.Errorf("expected the docker build to fall back to the conservative constant (%s), not the host's real capacity:\n%s", want, result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_dind_args_default_to_conservative_constant_when_unconfigured", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerfileWithoutDindArgsIsUnaffected(t *testing.T) {
	// A Dockerfile that declares neither ARG must not get either
	// build-arg, even when the building environment has a configured
	// runtimedindpod -- the regex-gated detection in
	// dockerfileConsumesDindCPULimit/dockerfileConsumesDindMemoryLimit
	// must not fire for an unrelated Dockerfile.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	appendEnvConfigForTest(t, setup, "team", "dev", "runtimedindpod:\n  cpu: \"6\"\n  memory: 24Gi\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "VERSION"), "1.0.0\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops", "Dockerfile"), "FROM alpine:3.22\n")
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "DIND_CPU_LIMIT") || strings.Contains(result.Combined, "DIND_MEMORY_LIMIT_MIB") {
		t.Errorf("expected no DIND build-args for a Dockerfile that declares neither ARG:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_without_dind_args_is_unaffected", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerfileCgroupParentAppliesInsideInjectedRuntimePod(t *testing.T) {
	// DIND_CPU_LIMIT (above) only fixes derivations *inside* the image --
	// the RUN-instruction containers themselves still escape the sidecar's
	// kubelet-declared CPU limit as sibling cgroups. Inside an injected
	// runtime pod (ERUN_TENANT/ERUN_ENVIRONMENT set, the same marker
	// inInjectedRuntimePod already uses elsewhere), every docker build must
	// carry --cgroup-parent so its RUN containers nest under the cgroup
	// `erun dind-entrypoint` mirrors this sidecar's own live cpu.max into.
	// Applies regardless of whether the Dockerfile declares DIND_CPU_LIMIT at
	// all, since the OS-level escape is not specific to that ARG.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "VERSION"), "1.0.0\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops", "Dockerfile"), "FROM alpine:3.22\n")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	envVars = append(envVars, stubDockerNoLocalImages(t, setup)...)
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		t.Fatalf("os.Hostname(): %v", err)
	}
	if !strings.Contains(result.Combined, "--cgroup-parent /docker/erun-build-cpu-cap-"+hostname) {
		t.Errorf("expected the docker build to carry --cgroup-parent for this pod:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_cgroup_parent_applies_inside_injected_runtime_pod", normalize.Apply(result.Combined))
}

func testBuildDryRunDockerfileCgroupParentAbsentOutsideInjectedRuntimePod(t *testing.T) {
	// A bare host build (no ERUN_TENANT/ERUN_ENVIRONMENT injected)
	// has no dind sidecar limit to escape, so it must not carry
	// --cgroup-parent -- forcing one would throttle a developer's own
	// machine for a problem that does not exist there.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "VERSION"), "1.0.0\n")
	mustWriteFile(t, filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops", "Dockerfile"), "FROM alpine:3.22\n")
	result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "--cgroup-parent") {
		t.Errorf("expected no --cgroup-parent outside an injected runtime pod:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_dockerfile_cgroup_parent_absent_outside_injected_runtime_pod", normalize.Apply(result.Combined))
}
