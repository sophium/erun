package orchestration

import (
	"github.com/sophium/erun/erun-common/base/core"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	deploypkg "github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveCurrentDeploySpecsForDockerTarget(ctx core.Context, store deploypkg.BuildDeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext deploypkg.DeployContextResolverFunc, now shared.NowFunc, target shared.DockerCommandTarget) ([]deploypkg.DeploySpec, error) {
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
