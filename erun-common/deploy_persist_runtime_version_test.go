package eruncommon

import (
	"io"
	"testing"
)

// TestPersistRuntimeVersionFromDeploySpecs pins how the runtime version advances
// after a deploy: a real rollout records the built-and-pushed version, while a
// cached no-op must NOT record the freshly minted version — it was never pushed —
// and instead heals to the version the release is actually running, leaving it
// untouched when that can't be read.
//
// Persist is a real, non-dry-run side effect, so it is unreachable from the
// dry-run integration binary and this white-box unit test is the contract owner.
func TestPersistRuntimeVersionFromDeploySpecs(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	const registry = "ghcr.io/sophium"

	runtimeSpec := func(skipHelm bool) DeploySpec {
		return DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{Name: "local"}},
			Deploy: HelmDeploySpec{
				ReleaseName:       RuntimeReleaseName(tenant),
				Version:           mintedVersion,
				ContainerRegistry: registry,
			},
			SkipHelm: skipHelm,
		}
	}
	fixedRunningVersion := func(version string) HelmReleaseVersionResolverFunc {
		return func(Context, string, string, string) (string, error) { return version, nil }
	}

	t.Run("rolled-out deploy persists the built and pushed version", func(t *testing.T) {
		var savedTenant string
		var saved *EnvConfig
		save := capturingSave(&savedTenant, &saved)
		persistOrFatal(t, Context{}, []DeploySpec{runtimeSpec(false)}, save, fixedRunningVersion(runningVersion))
		if saved == nil || savedTenant != tenant {
			t.Fatalf("expected save for tenant %q after a real rollout (saved=%v tenant=%q)", tenant, saved != nil, savedTenant)
		}
		if saved.RuntimeVersion != mintedVersion || saved.RuntimeRegistry != registry {
			t.Fatalf("saved {%q, %q}, want {%q, %q}", saved.RuntimeVersion, saved.RuntimeRegistry, mintedVersion, registry)
		}
	})

	t.Run("cached deploy (SkipHelm) heals to the running version, not the minted one", func(t *testing.T) {
		var savedTenant string
		var saved *EnvConfig
		save := capturingSave(&savedTenant, &saved)
		persistOrFatal(t, Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion(runningVersion))
		if saved == nil {
			t.Fatalf("a cached deploy must heal RuntimeVersion to the running version")
		}
		if saved.RuntimeVersion != runningVersion {
			t.Fatalf("RuntimeVersion = %q, want the running version %q (never the minted %q)", saved.RuntimeVersion, runningVersion, mintedVersion)
		}
	})

	t.Run("cached deploy (SkipHelm) with an unreadable running version leaves it unchanged", func(t *testing.T) {
		saved := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		// Empty string models a missing release / unavailable helm.
		persistOrFatal(t, Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion(""))
		if saved {
			t.Fatalf("could not read the running version; must not persist (would be a phantom)")
		}
	})

	t.Run("dry-run never persists or queries the cluster", func(t *testing.T) {
		saved := false
		resolverCalled := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		resolver := func(Context, string, string, string) (string, error) {
			resolverCalled = true
			return runningVersion, nil
		}
		persistOrFatal(t, Context{DryRun: true}, []DeploySpec{runtimeSpec(true)}, save, resolver)
		if saved || resolverCalled {
			t.Fatalf("dry-run must not persist (saved=%v) or query the cluster (resolverCalled=%v)", saved, resolverCalled)
		}
	})
}

func capturingSave(savedTenant *string, saved **EnvConfig) func(string, EnvConfig) error {
	return func(tn string, cfg EnvConfig) error {
		*savedTenant = tn
		c := cfg
		*saved = &c
		return nil
	}
}

func persistOrFatal(t *testing.T, ctx Context, specs []DeploySpec, save func(string, EnvConfig) error, resolve HelmReleaseVersionResolverFunc) {
	t.Helper()
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolve); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

// TestPersistRuntimeVersionFromDeploySpecsHealsRuntimeImage pins the fix for
// erun#1754: RuntimeVersion and RuntimeImage are one coordinate, so whatever
// writes the version back after a deploy must write the image in the same
// commit -- but only when the resolution actually knows a concrete name for
// it (PersistRuntimeImage), never clobbering an operator's own pin with a
// guess.
func TestPersistRuntimeVersionFromDeploySpecsHealsRuntimeImage(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"

	t.Run("heals a stale RuntimeImage left on the wrong line", func(t *testing.T) {
		var savedTenant string
		var saved *EnvConfig
		save := capturingSave(&savedTenant, &saved)
		spec := DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{
				Name:         "build",
				RuntimeImage: "ghcr.io/sophium/erun-devops",
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          RuntimeReleaseName(tenant),
				Version:              version,
				ContainerRegistry:    "ghcr.io/sophium",
				ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
				PersistRuntimeImage:  "frs-devops",
			},
		}
		persistOrFatal(t, Context{}, []DeploySpec{spec}, save, nil)
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
		var saved *EnvConfig
		save := capturingSave(new(string), &saved)
		spec := DeploySpec{
			Target: OpenResult{Tenant: "erun", EnvConfig: EnvConfig{
				Name:         "prod",
				RuntimeImage: "custom-fork-image",
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          RuntimeReleaseName("erun"),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/erun-devops:" + version,
				// PersistRuntimeImage left empty: the erun product's own
				// environments have no line of their own to persist.
			},
		}
		persistOrFatal(t, Context{}, []DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save (version changed from empty)")
		}
		if saved.RuntimeImage != "custom-fork-image" {
			t.Fatalf("RuntimeImage = %q, want left untouched at %q", saved.RuntimeImage, "custom-fork-image")
		}
	})

	t.Run("no-op when nothing changed, including RuntimeImage", func(t *testing.T) {
		saved := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		spec := DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{
				Name:                "build",
				RuntimeVersion:      version,
				RuntimeImage:        "frs-devops",
				RuntimeRunningImage: "ghcr.io/sophium/frs-devops:" + version,
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:          RuntimeReleaseName(tenant),
				Version:              version,
				ResolvedRuntimeImage: "ghcr.io/sophium/frs-devops:" + version,
				PersistRuntimeImage:  "frs-devops",
			},
		}
		persistOrFatal(t, Context{}, []DeploySpec{spec}, save, nil)
		if saved {
			t.Fatalf("expected no save: nothing changed")
		}
	})
}

// TestPersistRuntimeVersionFromDeploySpecsPersistsRuntimeChart pins the
// deploy-time twin of the RuntimeImage-persistence tests above: an operator's
// --runtime-chart override installs the named chart correctly for this one
// deploy (applyRuntimeChartOverride already rewrites the helm inputs), but
// until PersistRuntimeChart existed nothing wrote the coordinate back to
// EnvConfig.RuntimeChart -- so the next plain deploy or open re-derived the
// chart from the version and lost the operator's override.
func TestPersistRuntimeVersionFromDeploySpecsPersistsRuntimeChart(t *testing.T) {
	const tenant = "frs"
	const version = "1.0.86"
	const override = "oci://ghcr.io/sophium/charts/erun-devops:1.0.178"

	t.Run("persists an operator's --runtime-chart override", func(t *testing.T) {
		var saved *EnvConfig
		save := capturingSave(new(string), &saved)
		spec := DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{Name: "prod"}},
			Deploy: HelmDeploySpec{
				ReleaseName:         RuntimeReleaseName(tenant),
				Version:             version,
				ContainerRegistry:   "ghcr.io/sophium",
				PersistRuntimeChart: override,
			},
		}
		persistOrFatal(t, Context{}, []DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save")
		}
		if saved.RuntimeChart != override {
			t.Fatalf("RuntimeChart = %q, want persisted %q", saved.RuntimeChart, override)
		}
	})

	t.Run("empty PersistRuntimeChart never clobbers a recorded RuntimeChart", func(t *testing.T) {
		var saved *EnvConfig
		save := capturingSave(new(string), &saved)
		spec := DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{
				Name:         "prod",
				RuntimeChart: override,
			}},
			Deploy: HelmDeploySpec{
				ReleaseName:       RuntimeReleaseName(tenant),
				Version:           version,
				ContainerRegistry: "ghcr.io/sophium",
				// PersistRuntimeChart left empty: this deploy named no override.
			},
		}
		persistOrFatal(t, Context{}, []DeploySpec{spec}, save, nil)
		if saved == nil {
			t.Fatalf("expected a save (version changed from empty)")
		}
		if saved.RuntimeChart != override {
			t.Fatalf("RuntimeChart = %q, want left untouched at %q", saved.RuntimeChart, override)
		}
	})
}

// TestCachedDeployRunThenPersistHealsToRunningVersion drives RunDeploySpecs then
// PersistRuntimeVersionFromDeploySpecs — the exact sequence the CLI deploy command
// runs — to prove that a cached deploy never reaches the registry or rolls the pod
// and still heals the runtime version to the one actually running rather than the
// freshly minted, never-pushed version.
func TestCachedDeployRunThenPersistHealsToRunningVersion(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}

	spec := DeploySpec{
		Target:        OpenResult{Tenant: tenant, EnvConfig: EnvConfig{Name: "local"}},
		DeployContext: KubernetesDeployContext{ComponentName: RuntimeReleaseName(tenant)},
		Deploy: HelmDeploySpec{
			ReleaseName:       RuntimeReleaseName(tenant),
			Version:           mintedVersion,
			ContainerRegistry: "ghcr.io/sophium",
		},
		SkipHelm: true,
	}

	helmed := false
	var savedVersion string
	deploy := func(HelmDeployParams) error { helmed = true; return nil }
	save := func(_ string, cfg EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil }
	resolveRunning := func(Context, string, string, string) (string, error) { return runningVersion, nil }

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
