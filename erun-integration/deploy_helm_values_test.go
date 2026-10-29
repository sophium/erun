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

func testDeployDryRunStoppedEnvRendersReplicasZero(t *testing.T) {
	// A stopped env passes stopped=true so helm upgrade cannot restart it; only
	// `erun open` wakes an env.
	setup := env.New(t)
	fixture.SeedStoppedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_stopped_env_renders_replicas_zero", normalize.Apply(result.Combined))
}

func testDeployDryRunPlatformAccountBindsClusterAdmin(t *testing.T) {
	// platformAccount binds the env's SA to cluster-admin for in-pod terraform apply.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"platformaccount: true\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_platform_account_binds_cluster_admin", normalize.Apply(result.Combined))
}

func testDeployDryRunWithManagedCloudTracesHelmSetStrings(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "managed")
	envDir := filepath.Join(tenantDir, "prod")
	for _, dir := range []string{tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: prod\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "managed", "prod")
	result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_with_managed_cloud_traces_helm_set_strings", normalize.Apply(result.Combined))
}

func testDeployDryRunWithAWSClaudeModelsTracesSetStrings(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "managed")
	envDir := filepath.Join(tenantDir, "prod")
	for _, dir := range []string{tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: prod\n" +
		"repopath: " + setup.Cwd + "\n" +
		"kubernetescontext: edge\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"managedcloud: true\n" +
		"cloudprovideralias: dev\n" +
		"claude:\n" +
		"  usebedrock: true\n" +
		"  models: [opus, sonnet, haiku]\n" +
		"  maxoutputtokens: 8192\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	fixture.SeedDevopsRepo(t, setup, "managed", "prod")
	result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_with_aws_claude_models_traces_set_strings", normalize.Apply(result.Combined))
}

func testDeployDryRunWithOpenrouterGatewayTracesSetStrings(t *testing.T) {
	// Only the credential's Secret name reaches helm argv, never the token. This
	// env is also AWS, so model variables must not be emitted twice.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "managed")
	envDir := filepath.Join(tenantDir, "prod")
	for _, dir := range []string{tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("name: managed\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: prod\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: prod\n" +
		"repopath: " + setup.Cwd + "\n" +
		"kubernetescontext: edge\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"managedcloud: true\n" +
		"cloudprovideralias: dev\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	fixture.SeedDevopsRepo(t, setup, "managed", "prod")
	result := erun.Run(t, []string{"deploy", "managed", "prod", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_with_openrouter_gateway_traces_set_strings", normalize.Apply(result.Combined))
}

func testDeployDryRunSnapshotVersionResetsPostgresDatabase(t *testing.T) {
	// The docker stub answers every `manifest inspect` with 0, standing in for a registry.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	templates := filepath.Join(setup.Cwd, "team-devops", "k8s", "erun-backend-postgres", "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	mustWriteFile(t, filepath.Join(templates, "postgres.yaml"), strings.Join([]string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"spec:",
		"  template:",
		"    spec:",
		"      containers:",
		"        - image: registry.example/team/erun-backend-postgres:18.3 # pinned wrapper",
		"",
	}, "\n"))
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.5-snapshot-20260101000000",
		"--components", "erun-backend-postgres",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_snapshot_version_resets_postgres_database", normalize.Apply(result.Combined))
}

func testDeployDryRunReleasedVersionDoesNotResetPostgresDatabase(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDevopsBackendCharts(t, setup, "team", "dev")
	templates := filepath.Join(setup.Cwd, "team-devops", "k8s", "erun-backend-postgres", "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	mustWriteFile(t, filepath.Join(templates, "postgres.yaml"), strings.Join([]string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"spec:",
		"  template:",
		"    spec:",
		"      containers:",
		"        - image: registry.example/team/erun-backend-postgres:18.3 # pinned wrapper",
		"",
	}, "\n"))
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{
		"deploy", "team", "dev",
		"--version", "1.0.5",
		"--components", "erun-backend-postgres",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_released_version_does_not_reset_postgres_database", normalize.Apply(result.Combined))
}
