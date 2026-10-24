package release

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// RunPublish copies an already-built version's images from the FROM registry to each
// TO registry, using deploy's spec resolution but never running helm.
func RunPublish(ctx core.Context, store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target model.DeployTarget) error {
	specs, err := deploy.ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return err
	}
	total := 0
	for _, spec := range specs {
		copies, err := deploy.ResolveDeployRegistryCopies(spec)
		if err != nil {
			return err
		}
		total += len(copies)
		if err := deploy.RunDeployRegistryCopies(ctx, spec); err != nil {
			return err
		}
	}
	if total == 0 {
		return fmt.Errorf("publish: nothing to publish — mark a registry with the 'to' role and a 'from' source in .erun/config.yaml so the tested image can be mirrored to the shared registry")
	}
	return nil
}
