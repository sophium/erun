package deploy

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

type BuildDeployStore interface {
	DeployStore
	projectseam.DockerStore
}

func resolveDeployTarget(command string, scopesTenantByFlag bool, store DeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now projectseam.NowFunc, target model.DeployTarget) (configpkg.OpenResult, error) {
	result, err := resolveDeployTargetOpenResult(command, scopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := refuseHostEnvironmentOperation("deploy", result); err != nil {
		return configpkg.OpenResult{}, err
	}
	return result, nil
}

func resolveDeployTargetOpenResult(command string, scopesTenantByFlag bool, store DeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now projectseam.NowFunc, target model.DeployTarget) (configpkg.OpenResult, error) {
	store, findProjectRoot, _, _, _ = normalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

	if strings.TrimSpace(target.Tenant) != "" || strings.TrimSpace(target.Environment) != "" || strings.TrimSpace(target.RepoPath) != "" {
		if strings.TrimSpace(target.Tenant) == "" || strings.TrimSpace(target.Environment) == "" {
			return configpkg.OpenResult{}, fmt.Errorf("tenant and environment overrides are required together")
		}

		result, err := opentarget.ResolveOpenWithFinder(store, findProjectRoot, model.OpenParams{
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

	return opentarget.ResolveOpenWithFinder(store, findProjectRoot, model.OpenParams{
		UseDefaultTenant:          true,
		UseDefaultEnvironment:     true,
		Command:                   command,
		CommandScopesTenantByFlag: scopesTenantByFlag,
	})
}

// refuseHostEnvironmentOperation refuses a pod-requiring operation on a host
// environment. It checks ResolvedType, not !HasPod(), so untyped legacy envs still deploy.
func refuseHostEnvironmentOperation(operation string, target configpkg.OpenResult) error {
	if target.EnvConfig.ResolvedType() != configpkg.EnvironmentTypeHost {
		return nil
	}
	return fmt.Errorf("%s %s/%s: %s is a host environment — it has no pod and no cluster to %s against", operation, target.Tenant, target.Environment, target.Environment, operation)
}

// ResolveDeployTargetScope names the target for diagnostics before spec resolution;
// it never fails, degrading to whatever the target already carried.
func ResolveDeployTargetScope(store DeployStore, findProjectRoot projectseam.ProjectFinderFunc, target model.DeployTarget) (string, string) {
	tenant := strings.TrimSpace(target.Tenant)
	environment := strings.TrimSpace(target.Environment)
	if tenant != "" && environment != "" {
		return tenant, environment
	}
	resolved, err := resolveDeployTarget("", false, store, findProjectRoot, nil, nil, nil, target)
	if err != nil {
		return tenant, environment
	}
	return strings.TrimSpace(resolved.Tenant), strings.TrimSpace(resolved.Environment)
}

func normalizeDeployDependencies(store DeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now projectseam.NowFunc) (DeployStore, projectseam.ProjectFinderFunc, projectseam.BuildContextResolverFunc, DeployContextResolverFunc, projectseam.NowFunc) {
	if store == nil {
		store = configpkg.ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = dockerspec.ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

func NormalizeBuildDeployDependencies(store BuildDeployStore, findProjectRoot projectseam.ProjectFinderFunc, resolveDockerBuildContext projectseam.BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now projectseam.NowFunc) (BuildDeployStore, projectseam.ProjectFinderFunc, projectseam.BuildContextResolverFunc, DeployContextResolverFunc, projectseam.NowFunc) {
	if store == nil {
		store = configpkg.ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = configpkg.FindProjectRoot
	}
	if resolveDockerBuildContext == nil {
		resolveDockerBuildContext = dockerspec.ResolveDockerBuildContext
	}
	if resolveKubernetesDeployContext == nil {
		resolveKubernetesDeployContext = ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

// freezeNow pins now to one instant so every snapshot version minted in one
// deploy matches the persisted RuntimeVersion. It is idempotent.
func freezeNow(now projectseam.NowFunc) projectseam.NowFunc {
	if now == nil {
		now = time.Now
	}
	frozen := now()
	return func() time.Time { return frozen }
}

func ResolveDeployTargetForDockerTarget(store BuildDeployStore, findProjectRoot projectseam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DeployTarget, error) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return model.DeployTarget{}, err
	}
	if projectRoot == "" {
		return model.DeployTarget{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	environment, err := dockerbuild.ResolveDockerBuildEnvironment(store, findProjectRoot, projectRoot, target.Environment)
	if err != nil {
		return model.DeployTarget{}, err
	}

	tenant, err := resolveProjectTenantForRoot(store, projectRoot)
	if err != nil {
		return model.DeployTarget{}, err
	}

	return model.DeployTarget{
		Tenant:          tenant,
		Environment:     environment,
		RepoPath:        projectRoot,
		VersionOverride: strings.TrimSpace(target.VersionOverride),
		Force:           target.NoIncremental,
	}, nil
}

func resolveDeployVersionOverride(target model.DeployTarget, versionOverride string) string {
	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		return versionOverride
	}
	return strings.TrimSpace(target.VersionOverride)
}

// deployResetsDatabase arms the postgres reset for snapshots only; it takes no
// bool so callers cannot force it unconditional.
func deployResetsDatabase(version string) bool {
	return strings.Contains(strings.TrimSpace(version), "-snapshot-")
}

func resolveDeployContextForTarget(findProjectRoot projectseam.ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, target configpkg.OpenResult, componentName string) (model.KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return resolveDeployContext(findProjectRoot, resolveKubernetesDeployContext, componentName)
	}

	chartPath, err := helm.FindComponentHelmChartPath(target.RepoPath, componentName)
	if err != nil {
		return model.KubernetesDeployContext{}, err
	}

	return model.KubernetesDeployContext{
		Dir:           target.RepoPath,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

func resolveDeployContext(findProjectRoot projectseam.ProjectFinderFunc, resolveKubernetesDeployContext DeployContextResolverFunc, componentName string) (model.KubernetesDeployContext, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		context, err := resolveKubernetesDeployContext()
		if err != nil {
			return model.KubernetesDeployContext{}, err
		}
		if strings.TrimSpace(context.ChartPath) == "" || strings.TrimSpace(context.ComponentName) == "" {
			return model.KubernetesDeployContext{}, fmt.Errorf("helm chart not found in current component directory")
		}
		context.ComponentName = strings.TrimSpace(context.ComponentName)
		context.ChartPath = filepath.Clean(context.ChartPath)
		if err := ValidateHelmChartPath(context.ChartPath); err != nil {
			return model.KubernetesDeployContext{}, err
		}
		return context, nil
	}

	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, model.DockerCommandTarget{})
	if err != nil {
		return model.KubernetesDeployContext{}, err
	}
	if projectRoot == "" {
		return model.KubernetesDeployContext{}, fmt.Errorf("cannot determine project root for Helm deployment")
	}

	chartPath, err := helm.FindComponentHelmChartPath(projectRoot, componentName)
	if err != nil {
		return model.KubernetesDeployContext{}, err
	}

	return model.KubernetesDeployContext{
		Dir:           projectRoot,
		ComponentName: componentName,
		ChartPath:     chartPath,
	}, nil
}

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
	matches := make([]model.TenantConfig, 0, len(tenants))
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
		return "", opentarget.ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}
