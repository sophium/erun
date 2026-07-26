package eruncommon

import "fmt"

// RunPublish mirrors an already-built version's images from the FROM registry to
// each TO registry without deploying. It reuses deploy's spec resolution — so the
// image set and any cluster-registry concretization match exactly what a deploy
// would use — but runs only the from→to copy, never helm. This makes "publish the
// tested image to the shared registry once I'm happy" a deliberate action rather
// than a side effect of deploying. A version is required (deploy's contract), and
// the env must mark a FROM source and at least one TO destination.
func RunPublish(ctx Context, store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target DeployTarget) error {
	specs, err := ResolveCurrentDeploySpecs(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return err
	}
	total := 0
	for _, spec := range specs {
		copies, err := resolveDeployRegistryCopies(spec)
		if err != nil {
			return err
		}
		total += len(copies)
		if err := runDeployRegistryCopies(ctx, spec); err != nil {
			return err
		}
	}
	if total == 0 {
		return fmt.Errorf("publish: nothing to publish — mark a registry with the 'to' role and a 'from' source in .erun/config.yaml so the tested image can be mirrored to the shared registry")
	}
	return nil
}
