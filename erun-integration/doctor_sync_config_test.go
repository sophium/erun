package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorSyncConfigScenarios(t *testing.T) {
	t.Run("in_runtime_sync_config_in_sync_dry_run", testDoctorInRuntimeSyncConfigInSyncDryRun)
	t.Run("in_runtime_sync_config_missing_cloud_context_dry_run", testDoctorInRuntimeSyncConfigMissingCloudContextDryRun)
	t.Run("in_runtime_sync_config_legacy_remote_key_dry_run", testDoctorInRuntimeSyncConfigLegacyRemoteKeyDryRun)
	t.Run("in_runtime_sync_config_wrong_value_dry_run", testDoctorInRuntimeSyncConfigWrongValueDryRun)
	t.Run("in_runtime_sync_config_idle_drift_dry_run", testDoctorInRuntimeSyncConfigIdleDriftDryRun)
	t.Run("in_runtime_sync_config_legacy_repopath_no_spurious_drift_dry_run", testDoctorInRuntimeSyncConfigLegacyRepopathNoSpuriousDriftDryRun)
	t.Run("in_runtime_sync_config_no_tenant_env_noop", testDoctorInRuntimeSyncConfigNoTenantEnvNoop)
	t.Run("in_runtime_sync_config_real_run_reconciles_and_preserves", testDoctorInRuntimeSyncConfigRealRunReconcilesAndPreserves)
	t.Run("in_runtime_sync_config_real_run_idempotent", testDoctorInRuntimeSyncConfigRealRunIdempotent)
}

func testDoctorInRuntimeSyncConfigInSyncDryRun(t *testing.T) {
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_in_sync_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigMissingCloudContextDryRun(t *testing.T) {
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster",
		"ERUN_CLOUD_ENVIRONMENT=true", "ERUN_CLOUD_PROVIDER=aws",
		"ERUN_CLOUD_PROVIDER_ALIAS=team-aws", "ERUN_CLOUD_REGION=eu-west-1",
		"ERUN_CLOUD_CONTEXT_NAME=team-context", "ERUN_CLOUD_INSTANCE_ID=i-0abc")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_missing_cloud_context_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigLegacyRemoteKeyDryRun(t *testing.T) {
	// A legacy `remote: true` is drift even though decoding would migrate it.
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\nremote: true\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_legacy_remote_key_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigWrongValueDryRun(t *testing.T) {
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: stale-context\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_wrong_value_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigIdleDriftDryRun(t *testing.T) {
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 10m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster", "ERUN_IDLE_TIMEOUT=5m0s")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_idle_drift_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigLegacyRepopathNoSpuriousDriftDryRun(t *testing.T) {
	// repopath is excluded from the comparison.
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nrepopath: /home/erun/git/team\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_legacy_repopath_no_spurious_drift_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigNoTenantEnvNoop(t *testing.T) {
	setup := env.New(t)
	envVars := append(setup.Env(), "ERUN_REPO_REMOTE=true")
	result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "doctor/in_runtime_sync_config_no_tenant_env_noop", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeSyncConfigRealRunReconcilesAndPreserves(t *testing.T) {
	// Unprojected keys survive the rewrite.
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: stale-context\nruntimeversion: 1.2.3\nsshd:\n  enabled: true\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	result := erun.Run(t, []string{"doctor", "--sync-config"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/in_runtime_sync_config_real_run_reconciles_and_preserves", normalize.Apply(result.Combined))
	written := readFileForTest(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
	if !strings.Contains(written, "kubernetescontext: in-cluster") {
		t.Fatalf("projected key not reconciled:\n%s", written)
	}
	if !strings.Contains(written, "runtimeversion: 1.2.3") || !strings.Contains(written, "enabled: true") {
		t.Fatalf("unprojected keys not preserved:\n%s", written)
	}
}

func testDoctorInRuntimeSyncConfigRealRunIdempotent(t *testing.T) {
	setup := env.New(t)
	seedRuntimeEnvConfig(t, setup, "team", "dev",
		"name: dev\ntype: runtime\nkubernetescontext: stale-context\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
		"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
	first := erun.Run(t, []string{"doctor", "--sync-config"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if first.ExitCode != 0 {
		t.Fatalf("first run exit %d: %s", first.ExitCode, first.Combined)
	}
	second := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if !strings.Contains(second.Combined, "In-pod config matches the injected env") {
		t.Fatalf("second run not in-sync (perpetual drift):\n%s", second.Combined)
	}
	if strings.Contains(second.Combined, "write-yaml") {
		t.Fatalf("second run still traced writes:\n%s", second.Combined)
	}
}

// seedRuntimeEnvConfig writes the config the entrypoint would have written.
func seedRuntimeEnvConfig(t *testing.T, setup env.Setup, tenant, environment, envYAML string) {
	t.Helper()
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envYAML), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"), []byte("defaulttenant: "+tenant+"\n"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
}
