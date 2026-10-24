package deploy

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
)

func ResolveCurrentDeploySpecsForDockerTarget(ctx core.Context, store seam.BuildDeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target model.DockerCommandTarget) ([]seam.DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = deploypkg.NormalizeBuildDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	target, _, err := buildpkg.ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	deployTarget, err := deploypkg.ResolveDeployTargetForDockerTarget(store, findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	return deploypkg.ResolveCurrentDeploySpecsOrchestrated(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, deployTarget, true)
}
