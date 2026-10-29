package release

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/deploy/registrycopy"
	specpkg "github.com/sophium/erun/erun-common/primitive/deploy/spec"
)

// RunPublish copies an already-built version's images from the FROM registry to each
// TO registry, using deploy's spec resolution but never running helm.
func RunPublish(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target model.DeployTarget) error {
	specs, err := specpkg.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return err
	}
	total := 0
	for _, spec := range specs {
		copies, err := registrycopy.ResolveDeployRegistryCopies(spec)
		if err != nil {
			return err
		}
		total += len(copies)
		if err := registrycopy.RunDeployRegistryCopies(ctx, spec); err != nil {
			return err
		}
	}
	if total == 0 {
		return fmt.Errorf("publish: nothing to publish — mark a registry with the 'to' role and a 'from' source in .erun/config.yaml so the tested image can be mirrored to the shared registry")
	}
	return nil
}
