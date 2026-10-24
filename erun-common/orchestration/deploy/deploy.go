package deploy

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

func ResolveCurrentDeploySpecsForDockerTarget(ctx core.Context, store deploypkg.BuildDeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext deploypkg.DeployContextResolverFunc, now projectseam.NowFunc, target model.DockerCommandTarget) ([]deploypkg.DeploySpec, error) {
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
