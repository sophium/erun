package eruncommon

import (
	"errors"
	"testing"
)

func newRuntimeDeploySpec(tenant, environment, version string) DeploySpec {
	return DeploySpec{
		Target: OpenResult{
			Tenant:      tenant,
			Environment: environment,
			EnvConfig: EnvConfig{
				Name:           environment,
				RuntimeVersion: "1.0.51-snapshot-prior",
			},
		},
		Deploy: HelmDeploySpec{
			ReleaseName: RuntimeReleaseName(tenant),
			Tenant:      tenant,
			Environment: environment,
			Version:     version,
		},
	}
}

func TestPersistRuntimeVersionFromDeploySpecsSavesNewVersion(t *testing.T) {
	saved := map[string]EnvConfig{}
	save := func(tenant string, cfg EnvConfig) error {
		saved[tenant+"/"+cfg.Name] = cfg
		return nil
	}
	specs := []DeploySpec{newRuntimeDeploySpec("erun", "ux", "1.0.54")}
	if err := PersistRuntimeVersionFromDeploySpecs(Context{}, specs, save); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := saved["erun/ux"]
	if !ok {
		t.Fatalf("expected env config for erun/ux to be saved, got %+v", saved)
	}
	if got.RuntimeVersion != "1.0.54" {
		t.Fatalf("RuntimeVersion = %q, want 1.0.54", got.RuntimeVersion)
	}
}

func TestPersistRuntimeVersionFromDeploySpecsSkipsWhenUnchanged(t *testing.T) {
	calls := 0
	save := func(string, EnvConfig) error {
		calls++
		return nil
	}
	spec := newRuntimeDeploySpec("erun", "ux", "1.0.51-snapshot-prior")
	if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{spec}, save); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("save called %d times when version was unchanged; expected no-op", calls)
	}
}

func TestPersistRuntimeVersionFromDeploySpecsIgnoresNonRuntimeReleases(t *testing.T) {
	calls := 0
	save := func(string, EnvConfig) error {
		calls++
		return nil
	}
	// `erun deploy --components erun-backend-api` resolves a spec whose
	// ReleaseName is "erun-backend-api", not "<tenant>-devops". A backend
	// component deploy must not rewrite the runtime version.
	componentSpec := DeploySpec{
		Target: OpenResult{Tenant: "erun", Environment: "ux"},
		Deploy: HelmDeploySpec{ReleaseName: "erun-backend-api", Tenant: "erun", Environment: "ux", Version: "9.9.9"},
	}
	if err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{componentSpec}, save); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("save called %d times for a non-runtime release; expected no-op", calls)
	}
}

func TestPersistRuntimeVersionFromDeploySpecsSkipsDryRun(t *testing.T) {
	calls := 0
	save := func(string, EnvConfig) error {
		calls++
		return nil
	}
	spec := newRuntimeDeploySpec("erun", "ux", "1.0.54")
	if err := PersistRuntimeVersionFromDeploySpecs(Context{DryRun: true}, []DeploySpec{spec}, save); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("save called %d times in dry-run; expected no-op", calls)
	}
}

func TestPersistRuntimeVersionFromDeploySpecsSurfacesSaveError(t *testing.T) {
	want := errors.New("disk full")
	save := func(string, EnvConfig) error { return want }
	spec := newRuntimeDeploySpec("erun", "ux", "1.0.54")
	err := PersistRuntimeVersionFromDeploySpecs(Context{}, []DeploySpec{spec}, save)
	if !errors.Is(err, want) {
		t.Fatalf("expected save error to surface, got %v", err)
	}
}
