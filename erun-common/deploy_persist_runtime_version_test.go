package eruncommon

import (
	"io"
	"testing"
)

// TestPersistRuntimeVersionFromDeploySpecs pins how the env config's runtime
// version is advanced after a deploy:
//   - a real rollout records the version that was built and pushed;
//   - a cached no-op (SkipHelm: every image promoted from the fingerprint
//     cache, nothing rebuilt/pushed/rolled) must NOT record the freshly minted
//     Deploy.Version — it was never pushed — and instead heals to the version
//     the release is actually running (resolveDeployedVersion), which is
//     guaranteed pushed; when that can't be read it leaves the value untouched.
//
// Persist is a real, non-dry-run side effect (it early-returns on DryRun), so it
// is unreachable from the dry-run integration binary; this white-box unit test
// is the contract owner. See issue #475.
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
		save := func(tn string, cfg EnvConfig) error {
			savedTenant = tn
			c := cfg
			saved = &c
			return nil
		}
		// A real rollout records Deploy.Version and never consults the cluster.
		if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{runtimeSpec(false)}, save, fixedRunningVersion(runningVersion)); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved == nil || savedTenant != tenant {
			t.Fatalf("expected save for tenant %q after a real rollout (saved=%v tenant=%q)", tenant, saved != nil, savedTenant)
		}
		if saved.RuntimeVersion != mintedVersion || saved.RuntimeRegistry != registry {
			t.Fatalf("saved {%q, %q}, want {%q, %q}", saved.RuntimeVersion, saved.RuntimeRegistry, mintedVersion, registry)
		}
	})

	t.Run("cached deploy (SkipHelm) heals to the running version, not the minted one", func(t *testing.T) {
		var saved *EnvConfig
		save := func(_ string, cfg EnvConfig) error {
			c := cfg
			saved = &c
			return nil
		}
		if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion(runningVersion)); err != nil {
			t.Fatalf("persist: %v", err)
		}
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
		if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{runtimeSpec(true)}, save, fixedRunningVersion("")); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved {
			t.Fatalf("could not read the running version; must not persist (would be a phantom)")
		}
	})

	t.Run("dry-run never persists or queries the cluster", func(t *testing.T) {
		saved := false
		resolverCalled := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		resolver := func(Context, string, string, string) (string, error) { resolverCalled = true; return runningVersion, nil }
		if err := PersistRuntimeVersionFromDeploySpecs(Context{DryRun: true}, []DeploySpec{runtimeSpec(true)}, save, resolver); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved || resolverCalled {
			t.Fatalf("dry-run must not persist (saved=%v) or query the cluster (resolverCalled=%v)", saved, resolverCalled)
		}
	})
}

// TestCachedDeployRunThenPersistHealsToRunningVersion drives the real
// RunDeploySpecs orchestration for a cached runtime chart (SkipHelm) and then
// PersistRuntimeVersionFromDeploySpecs — the exact sequence the CLI deploy
// command runs. It proves the end-to-end cached-deploy path: RunDeploySpec
// short-circuits before building, pushing, or running helm (nothing reaches the
// registry or rolls the pod), and the persist step heals RuntimeVersion to the
// version the release is actually running rather than the freshly minted,
// never-pushed Deploy.Version. See issue #475.
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

	built, pushed, helmed := false, false, false
	var savedVersion string
	build := func(DockerBuildSpec, io.Writer, io.Writer) error { built = true; return nil }
	push := func(Context, DockerPushSpec) error { pushed = true; return nil }
	deploy := func(HelmDeployParams) error { helmed = true; return nil }
	save := func(_ string, cfg EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil }
	resolveRunning := func(Context, string, string, string) (string, error) { return runningVersion, nil }

	specs := []DeploySpec{spec}
	if err := RunDeploySpecs(ctx, specs, build, push, deploy); err != nil {
		t.Fatalf("RunDeploySpecs: %v", err)
	}
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolveRunning); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if built || pushed || helmed {
		t.Fatalf("cached (SkipHelm) deploy must not build/push/helm: built=%v pushed=%v helmed=%v", built, pushed, helmed)
	}
	if savedVersion != runningVersion {
		t.Fatalf("RuntimeVersion = %q, want healed to the running version %q (never the minted %q)", savedVersion, runningVersion, mintedVersion)
	}
}
