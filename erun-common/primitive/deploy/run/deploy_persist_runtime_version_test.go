package run

import (
	"io"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
)

func TestCachedDeployRunThenPersistHealsToRunningVersion(t *testing.T) {
	const tenant = "erun"
	const mintedVersion = "1.0.86-snapshot-20260608124610"
	const runningVersion = "1.0.86-snapshot-20260605090000"
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}

	spec := seam.DeploySpec{
		Target:        config.OpenResult{Tenant: tenant, EnvConfig: config.EnvConfig{Name: "local"}},
		DeployContext: model.KubernetesDeployContext{ComponentName: config.RuntimeReleaseName(tenant)},
		Deploy: seam.HelmDeploySpec{
			ReleaseName:       config.RuntimeReleaseName(tenant),
			Version:           mintedVersion,
			ContainerRegistry: "ghcr.io/sophium",
		},
		SkipHelm: true,
	}

	helmed := false
	var savedVersion string
	deploy := func(seam.HelmDeployParams) error { helmed = true; return nil }
	save := func(_ string, cfg config.EnvConfig) error { savedVersion = cfg.RuntimeVersion; return nil }
	resolveRunning := func(core.Context, string, string, string) (string, error) { return runningVersion, nil }

	specs := []seam.DeploySpec{spec}
	if err := RunDeploySpecs(ctx, specs, deploy); err != nil {
		t.Fatalf("RunDeploySpecs: %v", err)
	}
	if err := runtimeversion.PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolveRunning); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if helmed {
		t.Fatalf("cached (SkipHelm) deploy must not run helm: helmed=%v", helmed)
	}
	if savedVersion != runningVersion {
		t.Fatalf("RuntimeVersion = %q, want healed to the running version %q (never the minted %q)", savedVersion, runningVersion, mintedVersion)
	}
}
