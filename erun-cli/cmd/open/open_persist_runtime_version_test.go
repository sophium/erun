package open

import (
	"io"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// A cached no-op deploy mints an unpushed version, so open heals to the running one.
func TestDeployRuntimeHealsPersistedVersionOnCachedNoOp(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608164914" // minted by the cached resolve; never pushed
	const runningVersion = "1.0.86-snapshot-20260605090000"
	const stalePersisted = "1.0.86-snapshot-00000000000000"
	const registry = "ghcr.io/sophium"

	newRunner := func(resolver seam.HelmReleaseVersionResolverFunc, save func(string, config.EnvConfig) error) *resolvedOpenRunner {
		return &resolvedOpenRunner{
			ctx: core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)},
			result: config.OpenResult{
				Tenant:    tenant,
				EnvConfig: config.EnvConfig{Name: "local", RuntimeVersion: stalePersisted, RuntimeRegistry: registry},
			},
			options:                OpenOptions{SaveEnvConfig: save},
			resolveDeployedVersion: resolver,
		}
	}
	skipHelmExecution := deploy.DeploySpec{
		Target: config.OpenResult{
			Tenant:    tenant,
			EnvConfig: config.EnvConfig{Name: "local", RuntimeVersion: stalePersisted, RuntimeRegistry: registry},
		},
		SkipHelm: true,
		Deploy: deploy.HelmDeploySpec{
			ReleaseName:       config.RuntimeReleaseName(tenant),
			Namespace:         "erun-local",
			Version:           mintedVersion,
			ContainerRegistry: registry,
		},
	}

	t.Run("heals to the running version, never the minted one", func(t *testing.T) {
		var savedVersion string
		runner := newRunner(
			func(core.Context, string, string, string) (string, error) { return runningVersion, nil },
			func(_ string, cfg config.EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil },
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
			func(core.Context, string, string, string) (string, error) { return "", nil },
			func(string, config.EnvConfig) error {
				t.Fatalf("must not persist when the running version can't be read (would be a phantom)")
				return nil
			},
		)
		if err := runner.deployRuntime(skipHelmExecution); err != nil {
			t.Fatalf("deployRuntime: %v", err)
		}
	})
}

func TestDeployRuntimeHealsPersistedRuntimeImage(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"

	var saved *config.EnvConfig
	runner := &resolvedOpenRunner{
		ctx: core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)},
		result: config.OpenResult{
			Tenant:    tenant,
			EnvConfig: config.EnvConfig{Name: "build"},
		},
		options: OpenOptions{SaveEnvConfig: func(_ string, cfg config.EnvConfig) error {
			c := cfg
			saved = &c
			return nil
		}},
		resolveDeployedVersion: func(core.Context, string, string, string) (string, error) { return version, nil },
	}
	execution := deploy.DeploySpec{
		Target:   config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "build"}},
		SkipHelm: true,
		Deploy: deploy.HelmDeploySpec{
			ReleaseName:          config.RuntimeReleaseName(tenant),
			Version:              version,
			ContainerRegistry:    "ghcr.io/sophium",
			ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
			PersistRuntimeImage:  "frs-devops",
		},
	}
	if err := runner.deployRuntime(execution); err != nil {
		t.Fatalf("deployRuntime: %v", err)
	}
	if saved == nil {
		t.Fatalf("expected a save")
	}
	if saved.RuntimeImage != "frs-devops" {
		t.Fatalf("RuntimeImage = %q, want persisted %q", saved.RuntimeImage, "frs-devops")
	}
	if saved.RuntimeVersion != version {
		t.Fatalf("RuntimeVersion = %q, want %q", saved.RuntimeVersion, version)
	}
}
