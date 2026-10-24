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
	// deploy reconciles the operator's stop rather than overriding it: an env
	// carrying `stopped: true` threads --set stopped=true so the chart renders
	// replicas: 0, and a helm upgrade cannot silently restart a pod the
	// operator deliberately scaled away. deploy never wakes — `erun open`
	// does, which is also why deploy's version dedup skipping the helm call
	// cannot leave the run/stop state inconsistent. A running env emits no
	// such --set (the sibling dry_run_from_devops_cwd golden shows its absence).
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
	// An env flagged platformaccount:true threads --set platformAccount=true
	// into the runtime helm command, so the chart binds the env's SA to
	// cluster-admin (the <release>-platform ClusterRoleBinding) — the grant
	// that lets in-pod `erun terraform apply` of the cluster edge create
	// namespaces and CRDs. An off-by-default env emits no such --set (the
	// sibling dry_run_from_devops_cwd golden shows its absence).
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
	// Exercises deploy.applyCloudProviderDeployMetadata,
	// findCloudContextForKubernetesContext, cloudContextRegionFromName,
	// and the managed-cloud helm --set-string lines that come from
	// per-tenant cloud provider/context resolution.
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
	// Exercises deploy.helmClaudeSetArgs' Models + MaxOutputTokens
	// branches and NormalizedModels: claude.usebedrock/models/maxoutputtokens
	// resolve into the runtime chart's claude.useBedrock/availableModels/
	// maxOutputTokens helm --set-string args.
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
	// Exercises deploy.helmClaudeSetArgs' gateway branch: the erun-level
	// catalog in root config resolves into the runtime chart's
	// claude.openRouter* helm --set-string args. Only the credential's Secret
	// name travels — a token value never reaches helm argv. This env is also
	// AWS, so it pins that the gateway's available-models and model variables
	// are not emitted twice.
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
	// Locks the contract under pure deploy: installing a snapshot
	// version resolves ResetDatabase=true (deployResetsDatabase), so the
	// erun-backend-postgres chart's helm upgrade carries
	// --set api.postgres.reset=true. deploy installs by reference (no build);
	// the docker stub answers every `manifest inspect` with exit 0 so the
	// install-existing image-presence check passes without a registry.
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
	// Installing an already-published RELEASED version must not arm the
	// database wipe the way a snapshot does. This scenario is identical to
	// dry_run_snapshot_version_resets_postgres_database except the
	// version carries no -snapshot- marker, and must resolve
	// --set api.postgres.reset=false.
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
