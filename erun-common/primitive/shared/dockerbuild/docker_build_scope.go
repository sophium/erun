package dockerbuild

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func NormalizeDockerDependencies(store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, resolveBuildContext buildtarget.BuildContextResolverFunc, now buildtarget.NowFunc) (buildtarget.DockerStore, projectroot.ProjectFinderFunc, buildtarget.BuildContextResolverFunc, buildtarget.NowFunc) {
	if store == nil {
		store = config.ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	if resolveBuildContext == nil {
		resolveBuildContext = dockerspec.ResolveDockerBuildContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveBuildContext, now
}

// ConfiguredDockerDir fails on an unusable override rather than fall back to convention.
func ConfiguredDockerDir(projectRoot string) (string, bool, error) {
	paths, err := config.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	return ResolveAndValidateDockerDir(projectRoot, paths.Docker, "paths.docker")
}

func ResolveAndValidateDockerDir(projectRoot, override, configKey string) (string, bool, error) {
	dockerDir := config.ResolveProjectPath(projectRoot, override)
	if dockerDir == "" {
		return "", false, nil
	}
	ok, err := IsDockerBuildModuleDir(dockerDir)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("configured docker path %q (.erun/config.yaml %s) is not a docker build module: expected a directory named \"docker\" holding per-component build contexts", strings.TrimSpace(override), configKey)
	}
	return dockerDir, true, nil
}

func IsDockerBuildModuleDir(dir string) (bool, error) {
	buildContexts, err := dockerspec.ResolveDockerBuildContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, dockerspec.ErrDockerBuildContextNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(buildContexts) > 0, nil
}

func ResolveDockerBuildProjectRoot(findProjectRoot projectroot.ProjectFinderFunc, target model.DockerCommandTarget) (string, error) {
	if projectRoot := strings.TrimSpace(target.ProjectRoot); projectRoot != "" {
		return filepath.Clean(projectRoot), nil
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return projectRoot, nil
}

func ResolveDockerBuildEnvironment(store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, projectRoot, environment string) (string, error) {
	if environment = strings.TrimSpace(environment); environment != "" {
		return environment, nil
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	if environment, err := dockerBuildEnvironmentFromTenantConfigs(store, cleanProjectRoot); environment != "" || err != nil {
		return environment, err
	}
	return dockerBuildEnvironmentFromDetectedProject(store, findProjectRoot, cleanProjectRoot)
}

func dockerBuildEnvironmentFromTenantConfigs(store buildtarget.DockerStore, cleanProjectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}

	for _, tenantConfig := range tenants {
		if envName, err := dockerBuildEnvironmentFromTenantEnvs(store, tenantConfig.Name, cleanProjectRoot); err != nil {
			return "", err
		} else if envName != "" {
			return envName, nil
		}
	}
	return "", nil
}

func dockerBuildEnvironmentFromTenantEnvs(store buildtarget.DockerStore, tenant, cleanProjectRoot string) (string, error) {
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	for _, env := range envs {
		path := env.EffectiveLocalRepoPath()
		if path == "" {
			continue
		}
		if filepath.Clean(path) == cleanProjectRoot {
			return strings.TrimSpace(env.Name), nil
		}
	}
	return "", nil
}

func dockerBuildEnvironmentFromDetectedProject(store buildtarget.DockerStore, findProjectRoot projectroot.ProjectFinderFunc, cleanProjectRoot string) (string, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	if filepath.Clean(detectedProjectRoot) != cleanProjectRoot || strings.TrimSpace(tenant) == "" {
		return "", nil
	}

	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
}

func ResolveDockerBuildEnvConfigForProject(store buildtarget.DockerStore, projectRoot, wantEnv string) *config.EnvConfig {
	cleanRoot := filepath.Clean(projectRoot)

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return InjectedDockerBuildEnvConfig(nil)
	}
	wantEnv = strings.TrimSpace(wantEnv)
	for _, tenantConfig := range tenants {
		envs, err := store.ListEnvConfigs(tenantConfig.Name)
		if err != nil {
			continue
		}
		if match := matchDockerBuildEnvConfig(envs, wantEnv, cleanRoot); match != nil {
			return InjectedDockerBuildEnvConfig(match)
		}
	}
	return InjectedDockerBuildEnvConfig(nil)
}

// InjectedDockerBuildEnvConfig prefers chart-injected values in a runtime pod:
// the on-disk projection drifts, silently costing the build registry and
// disablebuildscript.
func InjectedDockerBuildEnvConfig(onDisk *config.EnvConfig) *config.EnvConfig {
	injected, ok := cloudconfig.ResolveInjectedRuntimeConfig(os.Getenv)
	if !ok {
		return onDisk
	}
	base := config.EnvConfig{}
	if onDisk != nil {
		base = *onDisk
	}
	merged := cloudconfig.OverlayInjectedEnvConfig(base, injected.Env)
	return &merged
}

func matchDockerBuildEnvConfig(envs []config.EnvConfig, wantEnv, cleanRoot string) *config.EnvConfig {
	for i := range envs {
		if wantEnv != "" && envs[i].Name != wantEnv {
			continue
		}
		path := strings.TrimSpace(envs[i].EffectiveLocalRepoPath())
		if path != "" && filepath.Clean(path) == cleanRoot {
			return &envs[i]
		}
	}
	return nil
}
