package spec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/deploytarget"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/published"
)

func resolveOpenRuntimeDeploySpec(ctx core.Context, store deploytarget.DeployStore, findProjectRoot projectroot.ProjectFinderFunc, resolveDockerBuildContext buildtarget.BuildContextResolverFunc, resolveKubernetesDeployContext deploytarget.DeployContextResolverFunc, now buildtarget.NowFunc, target config.OpenResult, allowLocalBuilds bool) (helmdeploy.DeploySpec, error) {
	// Before any --runtime-image override, a RuntimeImage here is a persisted memo, not this invocation's choice.
	if target.RemoteRepo() {
		return published.ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
	}

	for _, componentName := range chartref.RuntimeComponentNames(target.Tenant) {
		spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "", nil)
		if err == nil {
			spec.Deploy.ReleaseName = runtimetarget.RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return helmdeploy.DeploySpec{}, err
		}
	}

	return published.ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
}

func isHelmChartNotFoundForComponent(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "helm chart not found for component ")
}
