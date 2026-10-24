package deploy

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type BuildDeployStore interface {
	DeployStore
	shared.DockerStore
}

func resolveDeployTarget(command string, scopesTenantByFlag bool, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target DeployTarget) (configpkg.OpenResult, error) {
	result, err := resolveDeployTargetOpenResult(command, scopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := refuseHostEnvironmentOperation("deploy", result); err != nil {
		return configpkg.OpenResult{}, err
	}
	return result, nil
}

func resolveDeployTargetOpenResult(command string, scopesTenantByFlag bool, store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc, target DeployTarget) (configpkg.OpenResult, error) {
	store, findProjectRoot, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	if strings.TrimSpace(target.Tenant) != "" || strings.TrimSpace(target.Environment) != "" || strings.TrimSpace(target.RepoPath) != "" {
		if strings.TrimSpace(target.Tenant) == "" || strings.TrimSpace(target.Environment) == "" {
			return configpkg.OpenResult{}, fmt.Errorf("tenant and environment overrides are required together")
		}

		result, err := shared.ResolveOpenWithFinder(store, findProjectRoot, shared.OpenParams{
			Tenant:      strings.TrimSpace(target.Tenant),
			Environment: strings.TrimSpace(target.Environment),
		})
		if err != nil {
			return configpkg.OpenResult{}, err
		}
		if repoPath := strings.TrimSpace(target.RepoPath); repoPath != "" && filepath.Clean(result.RepoPath) != filepath.Clean(repoPath) {
			return configpkg.OpenResult{}, fmt.Errorf("resolved repo path %q does not match override %q", result.RepoPath, repoPath)
		}
		return result, nil
	}

	return shared.ResolveOpenWithFinder(store, findProjectRoot, shared.OpenParams{
		UseDefaultTenant:          true,
		UseDefaultEnvironment:     true,
		Command:                   command,
		CommandScopesTenantByFlag: scopesTenantByFlag,
	})
}

// refuseHostEnvironmentOperation refuses a pod-requiring operation (deploy,
// pin, terraform, ...) against a host environment: it has no pod and no
// cluster for the operation to act on. Naming the type in the message is the
// point — this is the resolution a "this operation does not apply" error must
// carry per root AGENTS.md § "Smooth, Seamless, No Dead Ends". Checked against
// ResolvedType rather than the broader !HasPod() so an env whose type predates
// the Type field (ResolvedType == "", see legacyEnvTypeFromRemoteSnapshot)
// keeps deploying exactly as it did before host existed, instead of being
// misreported as a host environment it never was.
func refuseHostEnvironmentOperation(operation string, target configpkg.OpenResult) error {
	if target.EnvConfig.ResolvedType() != configpkg.EnvironmentTypeHost {
		return nil
	}
	return fmt.Errorf("%s %s/%s: %s is a host environment — it has no pod and no cluster to %s against", operation, target.Tenant, target.Environment, target.Environment, operation)
}

// ResolveDeployTargetScope names the tenant/environment a deploy would act on
// before any spec work, so traces and failure headers can identify the target
// even when the deploy fails before spec resolution reaches it. Explicit
// overrides win; otherwise the current scope resolves. Naming the target is
// diagnostics, never a failure source, so an unresolvable scope degrades to
// whatever the target already carried.
func ResolveDeployTargetScope(store DeployStore, findProjectRoot shared.ProjectFinderFunc, target DeployTarget) (string, string) {
	tenant := strings.TrimSpace(target.Tenant)
	environment := strings.TrimSpace(target.Environment)
	if tenant != "" && environment != "" {
		return tenant, environment
	}
	// A nameless command on purpose: this is diagnostics that degrade quietly
	// rather than fail, so it has no failure message to name a recovery in.
	resolved, err := resolveDeployTarget("", false, store, findProjectRoot, nil, nil, nil, target)
	if err != nil {
		return tenant, environment
	}
	return strings.TrimSpace(resolved.Tenant), strings.TrimSpace(resolved.Environment)
}

func normalizeDeployDependencies(store DeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc) (DeployStore, shared.ProjectFinderFunc, shared.BuildContextResolverFunc, DeployContextResolverFunc, shared.NowFunc) {
	if store == nil {
		store = configpkg.ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = shared.ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

func NormalizeBuildDeployDependencies(store BuildDeployStore, findProjectRoot shared.ProjectFinderFunc, resolveDockerBuildContext shared.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now shared.NowFunc) (BuildDeployStore, shared.ProjectFinderFunc, shared.BuildContextResolverFunc, DeployContextResolverFunc, shared.NowFunc) {
	if store == nil {
		store = configpkg.ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = shared.ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

// freezeNow pins now to a single instant so every snapshot version minted while
// resolving one deploy (or build) shares the same timestamp. Otherwise each
// image's version calls now() independently, and across a multi-image deploy
// those timestamps drift apart — the runtime chart's persisted RuntimeVersion
// can end up differing from the tag actually built and pushed, a phantom version
// the deploy picker can never offer. freezeNow is idempotent, so applying it at
// more than one entrypoint on the same call path is safe.
func freezeNow(now shared.NowFunc) shared.NowFunc {
	if now == nil {
		now = time.Now
	}
	frozen := now()
	return func() time.Time { return frozen }
}

func ResolveDeployTargetForDockerTarget(store BuildDeployStore, findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (DeployTarget, error) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return DeployTarget{}, err
	}
	if projectRoot == "" {
		return DeployTarget{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	environment, err := shared.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return DeployTarget{}, err
	}

	tenant, err := resolveProjectTenantForRoot(store, projectRoot)
	if err != nil {
		return DeployTarget{}, err
	}

	return DeployTarget{
		Tenant:          tenant,
		Environment:     environment,
		RepoPath:        projectRoot,
		VersionOverride: strings.TrimSpace(target.VersionOverride),
		Force:           target.NoIncremental,
	}, nil
}

func resolveDeployVersionOverride(target DeployTarget, versionOverride string) string {
	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		return versionOverride
	}
	return strings.TrimSpace(target.VersionOverride)
}

// deployResetsDatabase reports whether installing version arms the postgres
// reset. A snapshot's data is disposable by convention; a released version's
// is not, so only the version decides — a bool parameter here would let a
// caller short-circuit it back to unconditional, which is the bug this
// signature exists to make impossible.
func deployResetsDatabase(version string) bool {
	return strings.Contains(strings.TrimSpace(version), "-snapshot-")
}

func resolveDeployContextForTarget(findProjectRoot shared.ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, target configpkg.OpenResult, componentName string) (shared.KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return resolveDeployContext(findProjectRoot, resolveKubernetesDeployContext, componentName)
	}

	chartPath, err := helm.FindComponentHelmChartPath(target.RepoPath, componentName)
	if err != nil {
		return shared.KubernetesDeployContext{}, err
	}

	return shared.KubernetesDeployContext{
		Dir:           target.RepoPath,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

func resolveDeployContext(findProjectRoot shared.ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, componentName string) (shared.KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		context, err := resolveKubernetesDeployContext()
		if err != nil {
			return shared.KubernetesDeployContext{}, err
		}
		if strings.TrimSpace(context.ChartPath) == "" || strings.TrimSpace(context.ComponentName) == "" {
			return shared.KubernetesDeployContext{}, fmt.Errorf("helm chart not found in current component directory")
		}
		context.ComponentName = strings.TrimSpace(context.ComponentName)
		context.ChartPath = filepath.Clean(context.ChartPath)
		if err := ValidateHelmChartPath(context.ChartPath); err != nil {
			return shared.KubernetesDeployContext{}, err
		}
		return context, nil
	}

	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, shared.DockerCommandTarget{})
	if err != nil {
		return shared.KubernetesDeployContext{}, err
	}
	if projectRoot == "" {
		return shared.KubernetesDeployContext{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	chartPath, err := helm.FindComponentHelmChartPath(projectRoot, componentName)
	if err != nil {
		return shared.KubernetesDeployContext{}, err
	}

	return shared.KubernetesDeployContext{
		Dir:           projectRoot,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

// tenantOwnsProjectRoot reports whether one of the tenant's envs records the
// given project root as its local repo path. A tenant whose envs aren't
// initialized simply doesn't own the root.
func tenantOwnsProjectRoot(store DeployStore, tenant, cleanProjectRoot string) (bool, error) {
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return false, nil
		}
		return false, err
	}
	for _, env := range envs {
		path := strings.TrimSpace(env.EffectiveLocalRepoPath())
		if path != "" && filepath.Clean(path) == cleanProjectRoot {
			return true, nil
		}
	}
	return false, nil
}

func resolveProjectTenantForRoot(store DeployStore, projectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return "", err
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	matches := make([]configpkg.TenantConfig, 0, len(tenants))
	for _, tenant := range tenants {
		owns, ownErr := tenantOwnsProjectRoot(store, tenant.Name, cleanProjectRoot)
		if ownErr != nil {
			return "", ownErr
		}
		if owns {
			matches = append(matches, tenant)
		}
	}

	defaultTenant, defaultErr := loadDefaultTenant(store)
	if defaultErr == nil {
		for _, tenant := range matches {
			if tenant.Name == defaultTenant {
				return tenant.Name, nil
			}
		}
	}

	if len(matches) == 1 {
		return matches[0].Name, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple tenants are configured for project %q", cleanProjectRoot)
	}

	return "", fmt.Errorf("no tenant is configured for project %q", cleanProjectRoot)
}

func loadDefaultTenant(store DeployStore) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", shared.ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}
