package eruncommon

import (
	"fmt"
	"strings"
)

// inPodBlindRuntimeOnlySelection reports whether this deploy is running inside
// the target environment's own runtime pod and still landed on the runtime-only
// fallback.
//
// A runtime pod's config store holds only the projection the chart injects (see
// `erun doctor --sync-config`), and that projection carries no deploy.components
// block. An in-pod resolve therefore finds no saved selection and reaches the
// "runtime chart alone" default for lack of information rather than by choice:
// the pod cannot tell a selection that is genuinely empty from the one the
// operator saved on the host. The default is the correct fallback off-pod, where
// the same resolve reads the operator's own config store.
func inPodBlindRuntimeOnlySelection(env func(string) string, resolvedTarget OpenResult, selected []string, selectionSource string) bool {
	if selectionSource != deploySelectionSourceDefault || len(selected) > 0 {
		return false
	}
	// Narrow by type: for this type the runtime chart is the whole environment,
	// so reaching the fallback is a real rollout decision rather than one chart
	// among others, and it earns the diagnosis below. The other types reach the
	// same fallback and are refused for it one layer on by the in-pod
	// runtime-chart guard (guardInPodRuntimeDeploy), so narrowing here leaves no
	// state unguarded.
	if resolvedTarget.EnvConfig.ResolvedType() != EnvironmentTypeRuntime {
		return false
	}
	podTenant, podEnvironment, inPod := injectedRuntimePodIdentity(env)
	if !inPod {
		return false
	}
	return podTenant == strings.TrimSpace(resolvedTarget.Tenant) &&
		podEnvironment == strings.TrimSpace(resolvedTarget.Environment)
}

// guardInPodBlindRuntimeOnlySelection refuses a deploy that would roll the
// runtime chart alone from inside that environment's own runtime pod.
//
// Rolling on that fallback upgrades one chart, leaves every component the
// operator selected on its previous version, and exits 0 — a silent partial
// upgrade of a production environment. Refusing and naming both
// remedies is the same fail-closed shape as the sibling in-pod guard
// (guardInPodRuntimeDeploy) and the saved-selection shadow guard: the
// deploy is not resolvable from here, so it is not attempted from here.
//
// Deliberately narrow: only a runtime environment, only its own pod, and only
// the empty-selection default. This refusal exists for the fallback that cannot
// be told apart from a genuinely empty selection; a selection that resolved to
// something still reaches the runtime chart, and is refused for reading the
// projection rather than the host store by the sibling guard one layer on
// (guardInPodRuntimeDeploy). An off-pod resolve reads the operator's own config
// store and is untouched by either.
func guardInPodBlindRuntimeOnlySelection(env func(string) string, resolvedTarget OpenResult, target DeployTarget, selected []string, selectionSource string) error {
	if !inPodBlindRuntimeOnlySelection(env, resolvedTarget, selected, selectionSource) {
		return nil
	}
	tenant := strings.TrimSpace(resolvedTarget.Tenant)
	environment := strings.TrimSpace(resolvedTarget.Environment)
	runtimeName := RuntimeReleaseName(tenant)
	return fmt.Errorf("deploy %s/%s: refusing to roll the runtime chart alone (%s) from inside this environment's own runtime pod — the in-pod config store is only the projection the chart injects (see `erun doctor --sync-config`), and it carries no deploy.components, so this process cannot tell a genuinely empty selection from the one saved on the host. Rolling on that fallback would upgrade the runtime chart, leave every component the operator selected on its previous version, and still report success. Run `erun deploy --tenant %s --environment %s --version %s` from the host CLI, where the saved selection resolves and the rollout is resolved from the host config store rather than from this pod",
		tenant, environment, runtimeName, tenant, environment, inPodGuardVersionHint(target.VersionOverride))
}
