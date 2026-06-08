package eruncommon

import (
	"io"
	"testing"
)

// TestPersistRuntimeVersionFromDeploySpecs pins the contract that the env
// config's runtime version is only advanced when the runtime chart was
// actually rolled out. A no-change `erun deploy` mints a fresh snapshot
// timestamp but promotes every image from the fingerprint cache (SkipHelm:
// nothing rebuilt, pushed, or helm-upgraded); recording that version left the
// env config — and the desktop runtime dialog — pointing at a tag that was
// never pushed to the registry and is not running, which the deploy picker
// can never offer because it gates on registry presence.
//
// Persist is a real, non-dry-run side effect (it early-returns on DryRun), so
// it is unreachable from the dry-run integration binary; this white-box unit
// test is the contract owner for the branch.
func TestPersistRuntimeVersionFromDeploySpecs(t *testing.T) {
	const tenant = "erun"
	const version = "1.0.86-snapshot-20260608124610"
	const registry = "ghcr.io/sophium"

	runtimeSpec := func(skipHelm bool) DeploySpec {
		return DeploySpec{
			Target: OpenResult{Tenant: tenant, EnvConfig: EnvConfig{Name: "local"}},
			Deploy: HelmDeploySpec{
				ReleaseName:       RuntimeReleaseName(tenant),
				Version:           version,
				ContainerRegistry: registry,
			},
			SkipHelm: skipHelm,
		}
	}

	t.Run("rolled-out deploy persists the version and registry", func(t *testing.T) {
		var savedTenant string
		var saved *EnvConfig
		save := func(tn string, cfg EnvConfig) error {
			savedTenant = tn
			c := cfg
			saved = &c
			return nil
		}
		if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{runtimeSpec(false)}, save); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved == nil {
			t.Fatalf("expected env config to be saved after a real rollout")
		}
		if savedTenant != tenant {
			t.Fatalf("saved tenant = %q, want %q", savedTenant, tenant)
		}
		if saved.RuntimeVersion != version {
			t.Fatalf("RuntimeVersion = %q, want %q", saved.RuntimeVersion, version)
		}
		if saved.RuntimeRegistry != registry {
			t.Fatalf("RuntimeRegistry = %q, want %q", saved.RuntimeRegistry, registry)
		}
	})

	t.Run("cached no-op deploy (SkipHelm) does not persist a phantom version", func(t *testing.T) {
		saved := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{runtimeSpec(true)}, save); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved {
			t.Fatalf("a SkipHelm deploy rolled nothing out; it must not persist a runtime version")
		}
	})

	t.Run("dry-run never persists", func(t *testing.T) {
		saved := false
		save := func(string, EnvConfig) error { saved = true; return nil }
		if err := PersistRuntimeVersionFromDeploySpecs(Context{DryRun: true}, []DeploySpec{runtimeSpec(false)}, save); err != nil {
			t.Fatalf("persist: %v", err)
		}
		if saved {
			t.Fatalf("dry-run must not persist")
		}
	})
}

// TestCachedDeployRunThenPersistDoesNothing drives the real RunDeploySpecs
// orchestration for a cached runtime chart (SkipHelm: every image promoted
// from the fingerprint cache) and then PersistRuntimeVersionFromDeploySpecs.
// It proves the end-to-end cached-deploy path: RunDeploySpec short-circuits
// before building, pushing, or running helm — so nothing reaches the registry
// or rolls the pod — and the freshly minted version is not persisted, so the
// env config can't end up pointing at a tag that was never pushed. This is the
// exact sequence the CLI deploy command runs (RunDeploySpecs then persist).
func TestCachedDeployRunThenPersistDoesNothing(t *testing.T) {
	const tenant = "erun"
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}

	spec := DeploySpec{
		Target:        OpenResult{Tenant: tenant, EnvConfig: EnvConfig{Name: "local"}},
		DeployContext: KubernetesDeployContext{ComponentName: RuntimeReleaseName(tenant)},
		Deploy: HelmDeploySpec{
			ReleaseName:       RuntimeReleaseName(tenant),
			Version:           "1.0.86-snapshot-20260608124610",
			ContainerRegistry: "ghcr.io/sophium",
		},
		SkipHelm: true,
	}

	built, pushed, helmed, saved := false, false, false, false
	build := func(DockerBuildSpec, io.Writer, io.Writer) error { built = true; return nil }
	push := func(Context, DockerPushSpec) error { pushed = true; return nil }
	deploy := func(HelmDeployParams) error { helmed = true; return nil }
	save := func(string, EnvConfig) error { saved = true; return nil }

	specs := []DeploySpec{spec}
	if err := RunDeploySpecs(ctx, specs, build, push, deploy); err != nil {
		t.Fatalf("RunDeploySpecs: %v", err)
	}
	if err := PersistRuntimeVersionFromDeploySpecs(ctx, specs, save); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if built || pushed || helmed {
		t.Fatalf("cached (SkipHelm) deploy must not build/push/helm: built=%v pushed=%v helmed=%v", built, pushed, helmed)
	}
	if saved {
		t.Fatalf("cached deploy rolled nothing out; it must not persist a runtime version")
	}
}
