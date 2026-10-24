package deploy

import (
	"io"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// A cached no-op must not persist the freshly minted, never-pushed version.
func TestPersistRuntimeVersionFromDeploySpecs(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	const registry = "ghcr.io/sophium"

	runtimeSpec := func(skipHelm bool) DeploySpec {
		return DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "local"}},
			Deploy: HelmDeploySpec{
				ReleaseName:       config.RuntimeReleaseName(tenant),
				Version:           mintedVersion,
				ContainerRegistry: registry,
			},
			SkipHelm: skipHelm,
		}
	}
	fixedRunningVersion := func(version string) HelmReleaseVersionResolverFunc {
		return func(core.Context, string, string, string) (string, error) { return version, nil }
	}

	t.Run("rolled-out deploy persists the built and pushed version", func(t *testing.T) {
		var savedTenant string
		var saved *config.EnvConfig
		save := capturingSave(&savedTenant, &saved)
		persistOrFatal(t, core.Context{}, []DeploySpec{runtimeSpec(false)}, save, fixedRunningVersion(runningVersion))
		if saved == nil || savedTenant != tenant {
			t.Fatalf("expected save for tenant %q after a real rollout (saved=%v tenant=%q)", tenant, saved != nil, savedTenant)
		}
		if saved.RuntimeVersion != mintedVersion || saved.RuntimeRegistry != registry {
			t.Fatalf("saved {%q, %q}, want {%q, %q}", saved.RuntimeVersion, saved.RuntimeRegistry, mintedVersion, registry)
		}
	})

	t.Run("cached deploy (SkipHelm) heals to the running version, not the minted one", func(t *testing.T) {
		var savedTenant string
		var saved *config.EnvConfig
		save := capturingSave(&savedTenant, &saved)
		persistOrFatal(t, core.Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion(runningVersion))
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
		persistOrFatal(t, core.Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion(""))
		if saved {
			t.Fatalf("could not read the running version; must not persist (would be a phantom)")
		}
	})

	t.Run("dry-run never persists or queries the cluster", func(t *testing.T) {
		saved := false
		resolverCalled := false
		save := func(string, config.EnvConfig) error { saved = true; return nil }
		resolver := func(core.Context, string, string, string) (string, error) {
			resolverCalled = true
			return runningVersion, nil
		}
		persistOrFatal(t, core.Context{DryRun: true}, []DeploySpec{runtimeSpec(true)}, save, resolver)
		if saved || resolverCalled {
			t.Fatalf("dry-run must not persist (saved=%v) or query the cluster (resolverCalled=%v)", saved, resolverCalled)
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

func persistOrFatal(t *testing.T, ctx core.Context, specs []DeploySpec, save func(string, config.EnvConfig) error, resolve HelmReleaseVersionResolverFunc) {
	t.Helper()
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolve); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

// Version and image are one coordinate; persist the image only when known.
func TestPersistRuntimeVersionFromDeploySpecsHealsRuntimeImage(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"

	t.Run("heals a stale RuntimeImage left on the wrong line", func(t *testing.T) {
		var savedTenant string
		var saved *config.EnvConfig
		save := capturingSave(&savedTenant, &saved)
		spec := DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{
				Name:         "build",
				RuntimeImage: "ghcr.io/sophium/erun-devops",
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          config.RuntimeReleaseName(tenant),
				Version:              version,
				ContainerRegistry:    "ghcr.io/sophium",
				ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
				PersistRuntimeImage:  "frs-devops",
			},
		}
		persistOrFatal(t, core.Context{}, []DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save")
		}
		if saved.RuntimeImage != "frs-devops" {
			t.Fatalf("RuntimeImage = %q, want healed to %q", saved.RuntimeImage, "frs-devops")
		}
		if saved.RuntimeRunningImage != "ghcr.io/sophium/frs-devops:"+version {
			t.Fatalf("RuntimeRunningImage = %q, want the resolved image", saved.RuntimeRunningImage)
		}
	})

	t.Run("empty PersistRuntimeImage never clobbers a recorded RuntimeImage", func(t *testing.T) {
		var saved *config.EnvConfig
		save := capturingSave(new(string), &saved)
		spec := DeploySpec{
			Target: config.OpenResult{Tenant: "erun", EnvConfig: config.EnvConfig{
				Name:         "prod",
				RuntimeImage: "custom-fork-image",
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          config.RuntimeReleaseName("erun"),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/erun-devops:" + version,
			},
		}
		persistOrFatal(t, core.Context{}, []DeploySpec{spec}, save, nil)
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
		spec := DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{
				Name:                "build",
				RuntimeVersion:      version,
				RuntimeImage:        "frs-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:" + version,
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          config.RuntimeReleaseName(tenant),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
				PersistRuntimeImage:  "frs-devops",
			},
		}
		persistOrFatal(t, core.Context{}, []DeploySpec{spec}, save, nil)
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
		spec := DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "prod"}},
			Deploy: HelmDeploySpec{
				ReleaseName:         config.RuntimeReleaseName(tenant),
				Version:             version,
				ContainerRegistry:   "ghcr.io/sophium",
				PersistRuntimeChart: override,
			},
		}
		persistOrFatal(t, core.Context{}, []DeploySpec{spec}, save, nil)
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
		spec := DeploySpec{
			Target: config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{
				Name:         "prod",
				RuntimeChart: override,
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:       config.RuntimeReleaseName(tenant),
				Version:           version,
				ContainerRegistry: "ghcr.io/sophium",
			},
		}
		persistOrFatal(t, core.Context{}, []DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save (version changed from empty)")
		}
		if saved.RuntimeChart != override {
			t.Fatalf("RuntimeChart = %q, want left untouched at %q", saved.RuntimeChart, override)
		}
	})
}

func TestCachedDeployRunThenPersistHealsToRunningVersion(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}

	spec := DeploySpec{
		Target:        config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "local"}},
		DeployContext: model.KubernetesDeployContext{ComponentName: config.RuntimeReleaseName(tenant)},
		Deploy: HelmDeploySpec{
			ReleaseName:       config.RuntimeReleaseName(tenant),
			Version:           mintedVersion,
			ContainerRegistry: "ghcr.io/sophium",
		},
		SkipHelm: true,
	}

	helmed := false
	var savedVersion string
	deploy := func(HelmDeployParams) error { helmed = true; return nil }
	save := func(_ string, cfg config.EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil }
	resolveRunning := func(core.Context, string, string, string) (string, error) { return runningVersion, nil }

	specs := []DeploySpec{spec}
	if err := RunDeploySpecs(ctx, specs, deploy); err != nil {
		t.Fatalf("RunDeploySpecs: %v", err)
	}
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolveRunning); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if helmed {
		t.Fatalf("cached (SkipHelm) deploy must not run helm: helmed=%v", helmed)
	}
	if savedVersion != runningVersion {
		t.Fatalf("RuntimeVersion = %q, want healed to the running version %q (never the minted %q)", savedVersion, runningVersion, mintedVersion)
	}
}
