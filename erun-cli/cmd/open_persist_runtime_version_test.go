package cmd

import (
	"io"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// TestDeployRuntimeSkipsPersistOnCachedNoOp pins the open-flow twin of the
// deploy-command guard in PersistRuntimeVersionFromDeploySpecs (#474). When the
// runtime deploy promoted every image from the fingerprint cache (SkipHelm —
// RunDeploySpec rebuilt, pushed, and rolled out nothing), execution.Deploy.Version
// is a freshly minted snapshot timestamp that was never pushed. Persisting it
// left the env config — and the desktop Manage dialog's "Runtime version" —
// pointing at a phantom version the deploy picker can never offer because it
// gates on registry presence.
//
// persistRuntimeVersion is a non-dry-run side effect (it early-returns on
// DryRun), so this branch is unreachable from the dry-run integration binary;
// this white-box test owns the contract, mirroring
// erun-common/deploy_persist_runtime_version_test.go.
func TestDeployRuntimeSkipsPersistOnCachedNoOp(t *testing.T) {
	const tenant = "erun"
	const deployedVersion = "1.0.86-snapshot-20260608163154"
	const phantomVersion = "1.0.86-snapshot-20260608163431"
	const registry = "ghcr.io/sophium"

	runner := &resolvedOpenRunner{
		ctx: common.Context{Logger: common.NewLoggerWithWriters(0, io.Discard, io.Discard)},
		result: common.OpenResult{
			Tenant:    tenant,
			EnvConfig: common.EnvConfig{Name: "local", RuntimeVersion: deployedVersion, RuntimeRegistry: registry},
		},
		options: openOptions{SaveEnvConfig: func(string, common.EnvConfig) error {
			t.Fatalf("a SkipHelm open rolled nothing out; it must not persist a runtime version")
			return nil
		}},
	}

	execution := common.DeploySpec{
		SkipHelm: true,
		Deploy: common.HelmDeploySpec{
			ReleaseName:       common.RuntimeReleaseName(tenant),
			Version:           phantomVersion,
			ContainerRegistry: registry,
		},
	}

	if err := runner.deployRuntime(execution); err != nil {
		t.Fatalf("deployRuntime: %v", err)
	}
	if got := runner.result.EnvConfig.RuntimeVersion; got != deployedVersion {
		t.Fatalf("RuntimeVersion = %q, want unchanged %q", got, deployedVersion)
	}
}

// TestPersistOpenRuntimeVersionWritesRolledOutVersion pins the positive half:
// when a rollout actually happened, the open flow records the deployed version
// and registry so the dialog and a later reopen address the same image.
func TestPersistOpenRuntimeVersionWritesRolledOutVersion(t *testing.T) {
	const tenant = "erun"
	const version = "1.0.86-snapshot-20260608163154"
	const registry = "ghcr.io/sophium"

	var savedTenant string
	var saved common.EnvConfig
	result := common.OpenResult{Tenant: tenant, EnvConfig: common.EnvConfig{Name: "local"}}

	updated, err := persistOpenRuntimeVersion(result, version, registry, func(tn string, cfg common.EnvConfig) error {
		savedTenant = tn
		saved = cfg
		return nil
	})
	if err != nil {
		t.Fatalf("persistOpenRuntimeVersion: %v", err)
	}
	if savedTenant != tenant {
		t.Fatalf("saved tenant = %q, want %q", savedTenant, tenant)
	}
	if saved.RuntimeVersion != version || saved.RuntimeRegistry != registry {
		t.Fatalf("saved = {%q, %q}, want {%q, %q}", saved.RuntimeVersion, saved.RuntimeRegistry, version, registry)
	}
	if updated.EnvConfig.RuntimeVersion != version {
		t.Fatalf("returned result RuntimeVersion = %q, want %q", updated.EnvConfig.RuntimeVersion, version)
	}
}
