package deploy

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/build/releasespec"
	"github.com/sophium/erun/erun-common/primitive/deploy/spec"
	targetpkg "github.com/sophium/erun/erun-common/primitive/deploy/target"
)

func ResolveCurrentDeploySpecsForDockerTarget(ctx core.Context, store deploytarget.BuildDeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target model.DockerCommandTarget) ([]helmdeploy.DeploySpec, error) {
	store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now = targetpkg.NormalizeBuildDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	target, _, err := releasespec.ResolveDockerBuildTarget(findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	deployTarget, err := targetpkg.ResolveDeployTargetForDockerTarget(store, findProjectRoot, target)
	if err != nil {
		return nil, err
	}

	return spec.ResolveCurrentDeploySpecsOrchestrated(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, deployTarget, true)
}
