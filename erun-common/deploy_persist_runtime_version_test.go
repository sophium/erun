package eruncommon

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
)

func capturingSave(savedTenant *string, saved **config.EnvConfig) func(string, config.EnvConfig) error {
	return func(tn string, cfg config.EnvConfig) error {
		*savedTenant = tn
		c := cfg
		*saved = &c
		return nil
	}
}

func persistOrFatal(t *testing.T, ctx core.Context, specs []deploypkg.DeploySpec, save func(string, config.EnvConfig) error, resolve deploypkg.HelmReleaseVersionResolverFunc) {
	t.Helper()
	if err := deploypkg.PersistRuntimeVersionFromDeploySpecs(ctx, specs, save, resolve); err != nil {
		t.Fatalf("persist: %v", err)
	}
}
