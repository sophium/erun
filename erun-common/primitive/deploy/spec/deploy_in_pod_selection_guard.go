package spec

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/components"
)

// inPodBlindRuntimeOnlySelection reports a runtime-only fallback reached inside
// the env's own pod, whose projected config has no deploy.components to read.
func inPodBlindRuntimeOnlySelection(env func(string) string, resolvedTarget config.OpenResult, selected []string, selectionSource string) bool {
	if selectionSource != components.DeploySelectionSourceDefault || len(selected) > 0 {
		return false
	}
	// Other types reaching this fallback are refused by guardInPodRuntimeDeploy.
	if resolvedTarget.EnvConfig.ResolvedType() != config.EnvironmentTypeRuntime {
		return false
	}
	podTenant, podEnvironment, inPod := core.InjectedRuntimePodIdentity(env)
	if !inPod {
		return false
	}
	return podTenant == strings.TrimSpace(resolvedTarget.Tenant) &&
		podEnvironment == strings.TrimSpace(resolvedTarget.Environment)
}

// guardInPodBlindRuntimeOnlySelection refuses that fallback, which would
// silently upgrade the runtime chart and leave components behind.
func guardInPodBlindRuntimeOnlySelection(env func(string) string, resolvedTarget config.OpenResult, target model.DeployTarget, selected []string, selectionSource string) error {
	if !inPodBlindRuntimeOnlySelection(env, resolvedTarget, selected, selectionSource) {
		return nil
	}
	tenant := strings.TrimSpace(resolvedTarget.Tenant)
	environment := strings.TrimSpace(resolvedTarget.Environment)
	runtimeName := runtimetarget.RuntimeReleaseName(tenant)
	return fmt.Errorf("deploy %s/%s: refusing to roll the runtime chart alone (%s) from inside this environment's own runtime pod — the in-pod config store is only the projection the chart injects (see `erun doctor --sync-config`), and it carries no deploy.components, so this process cannot tell a genuinely empty selection from the one saved on the host. Rolling on that fallback would upgrade the runtime chart, leave every component the operator selected on its previous version, and still report success. Run `erun deploy --tenant %s --environment %s --version %s` from the host CLI, where the saved selection resolves and the rollout is resolved from the host config store rather than from this pod",
		tenant, environment, runtimeName, tenant, environment, inPodGuardVersionHint(target.VersionOverride))
}
