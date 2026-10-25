package spec

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/published"
)

func resolveOpenRuntimeDeploySpec(ctx core.Context, store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target config.OpenResult, allowLocalBuilds bool) (seam.DeploySpec, error) {
	// Before any --runtime-image override, a RuntimeImage here is a persisted memo, not this invocation's choice.
	if target.RemoteRepo() {
		return published.ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
	}

	for _, componentName := range chartref.RuntimeComponentNames(target.Tenant) {
		spec, err := resolveDeploySpecForOpenResult(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "", nil)
		if err == nil {
			spec.Deploy.ReleaseName = config.RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return seam.DeploySpec{}, err
		}
	}

	return published.ResolvePublishedDevopsDeploySpecWithOverrides(ctx, target, "", "", false)
}

func isHelmChartNotFoundForComponent(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "helm chart not found for component ")
}
