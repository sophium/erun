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

func testDeployDryRunRuntimeEnvUnreachableRepoPathFallsBackToLocalPlan(t *testing.T) {
	// The env's repo path is an in-pod checkout absent on this host, so deploy
	// falls back to the project at its cwd to select the plan's components.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "environments:\n  dev:\n    k8s:\n      deployments:\n        - team-devops\n        - [erun-backend-postgres, erun-backend-db]\n        - erun-backend-api\n")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(root, "config.yaml"), "defaulttenant: team\n")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"), "projectroot: "+setup.Cwd+"\nname: team\ndefaultenvironment: dev\n")
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"),
		"name: dev\nrepopath: /nonexistent-remote/team\nkubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: runtime\n")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0,erun-backend-postgres:1.0.0,erun-backend-db:1.0.0,erun-backend-api:1.0.0")
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.0",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_env_unreachable_repo_path_falls_back_to_local_plan", normalize.Apply(result.Combined))
}

func testDeployDryRunRuntimeEnvMountSourceClonesAtReleaseRef(t *testing.T) {
	// MountSource opts a sourceless runtime env into a PVC checkout cloned at boot.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoVersion(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"mountsource: true\nrepourl: https://github.com/sophium/erun.git\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Normalization collapses "v1.0.0" to <VERSION>, so check the "v" on raw output.
	if !strings.Contains(result.Combined, "repoRef=v1.0.0") {
		t.Fatalf("expected repoRef=v1.0.0 (the release tag) in deploy command; got:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_runtime_env_mount_source_clones_at_release_ref", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvValuesOverlay(t *testing.T) {
	// A published chart has no directory, so the overlay lives in the env config dir.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	mustWriteFile(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "values.yaml"), "claude:\n  model: test-model\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_values_overlay", normalize.Apply(result.Combined))
}
