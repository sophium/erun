package cmd

import (
	"io"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// TestDeployRuntimeHealsPersistedVersionOnCachedNoOp guards a real gotcha: a
// cached no-op deploy mints a runtime version that was never pushed, so
// persisting it would point the env config at a phantom the deploy picker can
// never offer. The open flow instead heals to what the release is actually
// running, and leaves the persisted value untouched when that can't be read.
//
// This side effect is unreachable from the dry-run integration binary, so this
// white-box test owns the contract.
func TestDeployRuntimeHealsPersistedVersionOnCachedNoOp(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608164914" // minted by the cached resolve; never pushed
	const runningVersion = "1.0.86-snapshot-20260605090000"
	const stalePersisted = "1.0.86-snapshot-00000000000000"
	const registry = "ghcr.io/sophium"

	newRunner := func(resolver common.HelmReleaseVersionResolverFunc, save func(string, common.EnvConfig) error) *resolvedOpenRunner {
		return &resolvedOpenRunner{
			ctx: common.Context{Logger: common.NewLoggerWithWriters(0, io.Discard, io.Discard)},
			result: common.OpenResult{
				Tenant:    tenant,
				EnvConfig: common.EnvConfig{Name: "local", RuntimeVersion: stalePersisted, RuntimeRegistry: registry},
			},
			options:                openOptions{SaveEnvConfig: save},
			resolveDeployedVersion: resolver,
		}
	}
	skipHelmExecution := common.DeploySpec{
		SkipHelm: true,
		Deploy: common.HelmDeploySpec{
			ReleaseName:       common.RuntimeReleaseName(tenant),
			Namespace:         "erun-local",
			Version:           mintedVersion,
			ContainerRegistry: registry,
		},
	}

	t.Run("heals to the running version, never the minted one", func(t *testing.T) {
		var savedVersion string
		runner := newRunner(
			func(common.Context, string, string, string) (string, error) { return runningVersion, nil },
			func(_ string, cfg common.EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil },
		)
		if err := runner.deployRuntime(skipHelmExecution); err != nil {
			t.Fatalf("deployRuntime: %v", err)
		}
		if savedVersion != runningVersion {
			t.Fatalf("persisted %q, want the running version %q (never the minted %q)", savedVersion, runningVersion, mintedVersion)
		}
	})

	t.Run("unreadable running version leaves the persisted value unchanged", func(t *testing.T) {
		runner := newRunner(
			func(common.Context, string, string, string) (string, error) { return "", nil },
			func(string, common.EnvConfig) error {
				t.Fatalf("must not persist when the running version can't be read (would be a phantom)")
				return nil
			},
		)
		if err := runner.deployRuntime(skipHelmExecution); err != nil {
			t.Fatalf("deployRuntime: %v", err)
		}
	})
}
