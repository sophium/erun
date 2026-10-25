package target

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartfiles"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func ResolveDeployTarget(command string, scopesTenantByFlag bool, store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target model.DeployTarget) (configpkg.OpenResult, error) {
	result, err := resolveDeployTargetOpenResult(command, scopesTenantByFlag, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target)
	if err != nil {
		return configpkg.OpenResult{}, err
	}
	if err := RefuseHostEnvironmentOperation("deploy", result); err != nil {
		return configpkg.OpenResult{}, err
	}
	return result, nil
}

func resolveDeployTargetOpenResult(command string, scopesTenantByFlag bool, store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, target model.DeployTarget) (configpkg.OpenResult, error) {
	store, findProjectRoot, _, _, _ = NormalizeDeployDependencies(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now)

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

// RefuseHostEnvironmentOperation refuses a pod-requiring operation on a host
// environment. It checks ResolvedType, not !HasPod(), so untyped legacy envs still deploy.
func RefuseHostEnvironmentOperation(operation string, target configpkg.OpenResult) error {
	if target.EnvConfig.ResolvedType() != configpkg.EnvironmentTypeHost {
		return nil
	}
	return fmt.Errorf("%s %s/%s: %s is a host environment — it has no pod and no cluster to %s against", operation, target.Tenant, target.Environment, target.Environment, operation)
}

// ResolveDeployTargetScope names the target for diagnostics before spec resolution;
// it never fails, degrading to whatever the target already carried.
func ResolveDeployTargetScope(store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, target model.DeployTarget) (string, string) {
	tenant := strings.TrimSpace(target.Tenant)
	environment := strings.TrimSpace(target.Environment)
	if tenant != "" && environment != "" {
		return tenant, environment
	}
	resolved, err := ResolveDeployTarget("", false, store, findProjectRoot, nil, nil, nil, target)
	if err != nil {
		return tenant, environment
	}
	return strings.TrimSpace(resolved.Tenant), strings.TrimSpace(resolved.Environment)
}

func NormalizeDeployDependencies(store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc) (seam.DeployStore, seam.ProjectFinderFunc, seam.BuildContextResolverFunc, seam.DeployContextResolverFunc, seam.NowFunc) {
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
		resolveKubernetesDeployContext = kubecheck.ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

func NormalizeBuildDeployDependencies(store seam.BuildDeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc) (seam.BuildDeployStore, seam.ProjectFinderFunc, seam.BuildContextResolverFunc, seam.DeployContextResolverFunc, seam.NowFunc) {
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
		resolveKubernetesDeployContext = kubecheck.ResolveKubernetesDeployContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now
}

// FreezeNow pins now to one instant so every snapshot version minted in one
// deploy matches the persisted RuntimeVersion. It is idempotent.
func FreezeNow(now seam.NowFunc) seam.NowFunc {
	if now == nil {
		now = time.Now
	}
	frozen := now()
	return func() time.Time { return frozen }
}

func ResolveDeployTargetForDockerTarget(store seam.BuildDeployStore, findProjectRoot seam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DeployTarget, error) {
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

func ResolveDeployVersionOverride(target model.DeployTarget, versionOverride string) string {
	if versionOverride = strings.TrimSpace(versionOverride); versionOverride != "" {
		return versionOverride
	}
	return strings.TrimSpace(target.VersionOverride)
}

func ResolveDeployContextForTarget(findProjectRoot seam.ProjectFinderFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, target configpkg.OpenResult, componentName string) (model.KubernetesDeployContext, error) {
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

func resolveDeployContext(findProjectRoot seam.ProjectFinderFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, componentName string) (model.KubernetesDeployContext, error) {
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
		if err := chartfiles.ValidateHelmChartPath(context.ChartPath); err != nil {
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

func tenantOwnsProjectRoot(store seam.DeployStore, tenant, cleanProjectRoot string) (bool, error) {
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

func resolveProjectTenantForRoot(store seam.DeployStore, projectRoot string) (string, error) {
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

func loadDefaultTenant(store seam.DeployStore) (string, error) {
	toolConfig, _, err := store.LoadERunConfig()
	if err != nil {
		return "", err
	}
	if toolConfig.DefaultTenant == "" {
		return "", opentarget.ErrDefaultTenantNotConfigured
	}
	return toolConfig.DefaultTenant, nil
}
