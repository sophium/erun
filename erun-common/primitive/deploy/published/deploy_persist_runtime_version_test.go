package published

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeversion"
)

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
	if err := runtimeversion.PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolve); err != nil {
		t.Fatalf("persist: %v", err)
	}
}
