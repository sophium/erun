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
	// The DIND ARGs come from the env's configured runtimedindpod; the
	// non-default 6 CPU / 24Gi proves the fallback is not used.
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
	// Unconfigured falls back to erun-common's fixed constant, never the host's capacity.
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
	// RUN containers otherwise escape the sidecar's CPU limit as sibling
	// cgroups, so in-pod builds carry --cgroup-parent regardless of ARGs.
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
