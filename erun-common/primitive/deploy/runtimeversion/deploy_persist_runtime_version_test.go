package runtimeversion

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
)

// A cached no-op must not persist the freshly minted, never-pushed version.
func TestPersistRuntimeVersionFromDeploySpecs(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	const registry = "ghcr.io/sophium"

	runtimeSpec := func() seam.DeploySpec {
		return seam.DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "local"}},
			Deploy: seam.HelmDeploySpec{
				ReleaseName:       config.RuntimeReleaseName(tenant),
				Version:           mintedVersion,
				ContainerRegistry: registry,
			},
			SkipHelm: true,
		}
	}
	fixedRunningVersion := func(version string) seam.HelmReleaseVersionResolverFunc {
		return func(core.Context, string, string, string) (string, error) { return version, nil }
	}

	t.Run("cached deploy (SkipHelm) heals to the running version, not the minted one", func(t *testing.T) {
		var savedTenant string
		var saved *config.EnvConfig
		save := capturingSave(&savedTenant, &saved)
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{runtimeSpec()}, save, fixedRunningVersion(runningVersion))
		if saved == nil {
			t.Fatalf("a cached deploy must heal RuntimeVersion to the running version")
		}
		if saved.RuntimeVersion != runningVersion {
			t.Fatalf("RuntimeVersion = %q, want the running version %q (never the minted %q)", saved.RuntimeVersion, runningVersion, mintedVersion)
		}
	})

	t.Run("cached deploy (SkipHelm) with an unreadable running version leaves it unchanged", func(t *testing.T) {
		saved := false
		save := func(string, config.EnvConfig) error { saved = true; return nil }
		// Empty models a missing release or unavailable helm.
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{runtimeSpec()}, save, fixedRunningVersion(""))
		if saved {
			t.Fatalf("could not read the running version; must not persist (would be a phantom)")
		}
	})
}

func capturingSave(savedTenant *string, saved **config.EnvConfig) func(string, config.EnvConfig) error {
	return func(tn string, cfg config.EnvConfig) error {
		*savedTenant = tn
		c := cfg
		*saved = &c
		return nil
	}
}

func persistOrFatal(t *testing.T, ctx core.Context, specs []seam.DeploySpec, save func(string, config.EnvConfig) error, resolve seam.HelmReleaseVersionResolverFunc) {
	t.Helper()
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolve); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

// Version and image are one coordinate; persist the image only when known.
func TestPersistRuntimeVersionFromDeploySpecsHealsRuntimeImage(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"

	t.Run("empty PersistRuntimeImage never clobbers a recorded RuntimeImage", func(t *testing.T) {
		var saved *config.EnvConfig
		save := capturingSave(new(string), &saved)
		spec := seam.DeploySpec{
			Target: config.OpenResult{Tenant: "erun", EnvConfig: config.EnvConfig{
				Name:         "prod",
				RuntimeImage: "custom-fork-image",
			}},
			Deploy: seam.HelmDeploySpec{
				ReleaseName:          config.RuntimeReleaseName("erun"),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/erun-devops:" + version,
			},
		}
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save (version changed from empty)")
		}
		if saved.RuntimeImage != "custom-fork-image" {
			t.Fatalf("RuntimeImage = %q, want left untouched at %q", saved.RuntimeImage, "custom-fork-image")
		}
	})

	t.Run("no-op when nothing changed, including RuntimeImage", func(t *testing.T) {
		saved := false
		save := func(string, config.EnvConfig) error { saved = true; return nil }
		spec := seam.DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{
				Name:                "build",
				RuntimeVersion:      version,
				RuntimeImage:        "frs-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:" + version,
			}},
			Deploy: seam.HelmDeploySpec{
				ReleaseName:          config.RuntimeReleaseName(tenant),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
				PersistRuntimeImage:  "frs-devops",
			},
		}
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{spec}, save, nil)
		if saved {
			t.Fatalf("expected no save: nothing changed")
		}
	})
}

func TestPersistRuntimeVersionFromDeploySpecsPersistsRuntimeChart(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"
	const override = "oci://ghcr.io/sophium/charts/erun-devops:1.0.178"

	t.Run("persists an operator's --runtime-chart override", func(t *testing.T) {
		var saved *config.EnvConfig
		save := capturingSave(new(string), &saved)
		spec := seam.DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "prod"}},
			Deploy: seam.HelmDeploySpec{
				ReleaseName:         config.RuntimeReleaseName(tenant),
				Version:             version,
				ContainerRegistry:   "ghcr.io/sophium",
				PersistRuntimeChart: override,
			},
		}
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save")
		}
		if saved.RuntimeChart != override {
			t.Fatalf("RuntimeChart = %q, want persisted %q", saved.RuntimeChart, override)
		}
	})

	t.Run("empty PersistRuntimeChart never clobbers a recorded RuntimeChart", func(t *testing.T) {
		var saved *config.EnvConfig
		save := capturingSave(new(string), &saved)
		spec := seam.DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{
				Name:         "prod",
				RuntimeChart: override,
			}},
			Deploy: seam.HelmDeploySpec{
				ReleaseName:       config.RuntimeReleaseName(tenant),
				Version:           version,
				ContainerRegistry: "ghcr.io/sophium",
			},
		}
		persistOrFatal(t, core.Context{}, []seam.DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save (version changed from empty)")
		}
		if saved.RuntimeChart != override {
			t.Fatalf("RuntimeChart = %q, want left untouched at %q", saved.RuntimeChart, override)
		}
	})
}
