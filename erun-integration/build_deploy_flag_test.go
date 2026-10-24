package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildDeployFlagScenarios(t *testing.T) {
	t.Run("dry_run_build_deploy_resolves_docker_target_deploy_specs", testBuildDryRunBuildDeployResolvesDockerTargetDeploySpecs)
	t.Run("dry_run_build_deploy_erun_devops_component_resolves_runtime_image_memo", testBuildDryRunBuildDeployErunDevopsComponentResolvesRuntimeImageMemo)
	t.Run("dry_run_build_deploy_default_tenant_breaks_project_tie", testBuildDryRunBuildDeployDefaultTenantBreaksProjectTie)
	t.Run("dry_run_build_deploy_ambiguous_tenants_error", testBuildDryRunBuildDeployAmbiguousTenantsError)
	t.Run("build_deploy_with_project_build_script_errors", testBuildBuildDeployWithProjectBuildScriptErrors)
}

func testBuildDryRunBuildDeployResolvesDockerTargetDeploySpecs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_build_deploy_resolves_docker_target_deploy_specs", normalize.Apply(result.Combined))
}

func testBuildDryRunBuildDeployErunDevopsComponentResolvesRuntimeImageMemo(t *testing.T) {
	// When the built component is the tenant's own erun-devops, its image
	// doubles as the runtime-line memo.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "erun", "dev")
	fixture.SeedDevopsRepo(t, setup, "erun", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "erun")
	fixture.SeedGitRepo(t, setup.Cwd)
	dockerDir := filepath.Join(setup.Cwd, "erun-devops", "docker", "erun-devops")
	result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_build_deploy_erun_devops_component_resolves_runtime_image_memo", normalize.Apply(result.Combined))
}

func testBuildDryRunBuildDeployDefaultTenantBreaksProjectTie(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	// Sorts after "team" so the legacy fallback resolves team/dev first.
	otherDir := filepath.Join(setup.ConfigHome, "erun", "zz-extra")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", otherDir, err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "config.yaml"),
		[]byte("projectroot: "+setup.Cwd+"\nname: zz-extra\n"), 0o644); err != nil {
		t.Fatalf("zz-extra tenant cfg: %v", err)
	}
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_build_deploy_default_tenant_breaks_project_tie", normalize.Apply(result.Combined))
}

func testBuildDryRunBuildDeployAmbiguousTenantsError(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
	fixture.SeedGitRepo(t, setup.Cwd)
	// cwd->tenant matching uses envs' localRepoPath, so the other tenant
	// needs an env recording this cwd.
	otherEnvDir := filepath.Join(setup.ConfigHome, "erun", "other", "dev")
	if err := os.MkdirAll(otherEnvDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", otherEnvDir, err)
	}
	if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "other", "config.yaml"),
		[]byte("name: other\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("other tenant cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherEnvDir, "config.yaml"),
		[]byte("name: dev\nlocalrepopath: "+setup.Cwd+"\ntype: local-agent\n"), 0o644); err != nil {
		t.Fatalf("other env cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"),
		[]byte("defaulttenant: elsewhere\n"), 0o644); err != nil {
		t.Fatalf("root cfg: %v", err)
	}
	dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
	result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for ambiguous tenants, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_build_deploy_ambiguous_tenants_error", normalize.Apply(result.Combined))
}

func testBuildBuildDeployWithProjectBuildScriptErrors(t *testing.T) {
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", "build.sh")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
	result := erun.Run(t, []string{"build", "--deploy", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for --deploy with a build script, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/build_deploy_with_project_build_script_errors", normalize.Apply(result.Combined))
}
