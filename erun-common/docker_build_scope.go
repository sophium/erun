package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func normalizeDockerDependencies(store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc) (DockerStore, ProjectFinderFunc, BuildContextResolverFunc, NowFunc) {
	if store == nil {
		store = ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}
	if now == nil {
		now = time.Now
	}
	return store, findProjectRoot, resolveBuildContext, now
}

func ResolveCurrentDockerBuildContexts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) ([]DockerBuildContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return []DockerBuildContext{buildContext}, nil
	}

	if buildContexts, err := ResolveDockerBuildContextsAtDir(buildContext.Dir); err == nil {
		return buildContexts, nil
	}

	dockerDir, ok, err := resolveCurrentDevopsDockerDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveDockerBuildContextsAtDir(dockerDir)
	}

	return nil, ErrDockerBuildContextNotFound
}

func resolveCurrentDevopsDockerDir(findProjectRoot ProjectFinderFunc, dir string, target DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}

	// A configured paths.docker override (or a selected components: entry) is
	// authoritative: it wins over the -devops cwd shortcut and the project-root
	// convention scan, and applies from any cwd inside the project.
	if projectRoot != "" {
		if configured, ok, err := resolveComponentAwareDockerDir(projectRoot, target.Component); err != nil || ok {
			return configured, ok, err
		}
	}

	return resolveConventionDevopsDockerDir(findProjectRoot, dir, projectRoot)
}

// resolveConventionDevopsDockerDir is the convention discovery used when no
// paths.docker override applies: the cwd -devops shortcut, then the project-root
// <tenant>-devops/docker scan (only when cwd is the project root).
func resolveConventionDevopsDockerDir(findProjectRoot ProjectFinderFunc, dir, projectRoot string) (string, bool, error) {
	dockerDir := filepath.Join(dir, "docker")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isDockerBuildModuleDir(dockerDir); err != nil {
			return "", false, err
		} else if ok {
			return dockerDir, true, nil
		}
	}

	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
}

// configuredDockerDir resolves the project-global paths.docker override. It
// returns (dir, true, nil) when configured and a usable docker build module,
// (,false,nil) when unset, and an error when configured but not a docker build
// module — a misconfigured override fails loudly rather than silently falling
// back to convention.
func configuredDockerDir(projectRoot string) (string, bool, error) {
	paths, err := loadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	return resolveAndValidateDockerDir(projectRoot, paths.Docker, "paths.docker")
}

// resolveComponentAwareDockerDir resolves the docker build root a build/push
// command should use: the selected components: entry when the project
// declares one or more (see resolveProjectComponent), otherwise the unchanged
// paths.docker override via configuredDockerDir.
func resolveComponentAwareDockerDir(projectRoot, selectedComponent string) (string, bool, error) {
	name, paths, ok, err := resolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return configuredDockerDir(projectRoot)
	}
	return resolveAndValidateDockerDir(projectRoot, paths.Docker, fmt.Sprintf("components.%s.docker", name))
}

func resolveAndValidateDockerDir(projectRoot, override, configKey string) (string, bool, error) {
	dockerDir := resolveProjectPath(projectRoot, override)
	if dockerDir == "" {
		return "", false, nil
	}
	ok, err := isDockerBuildModuleDir(dockerDir)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("configured docker path %q (.erun/config.yaml %s) is not a docker build module: expected a directory named \"docker\" holding per-component build contexts", strings.TrimSpace(override), configKey)
	}
	return dockerDir, true, nil
}

func resolveProjectRootDevopsDockerDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	dockerDir, ok, err := detectedProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return dockerDir, ok, err
	}

	candidates, err := findDevopsDockerDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops docker directories found under project root")
	}
}

func detectedProjectRootDevopsDockerDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
		return "", false, nil
	}
	dockerDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "docker")
	if ok, err := isDockerBuildModuleDir(dockerDir); err != nil {
		return "", false, err
	} else if ok {
		return dockerDir, true, nil
	}
	return "", false, nil
}

func findDevopsDockerDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		dockerDir := filepath.Join(projectRoot, entry.Name(), "docker")
		ok, err := isDockerBuildModuleDir(dockerDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, dockerDir)
		}
	}
	return candidates, nil
}

// projectHasDevopsFolder is a bare presence check — it does not require a
// buildable docker module — so the build-env advisory fires only when there is
// no devops module at all, not when one exists but is still being set up.
func projectHasDevopsFolder(projectRoot string) (bool, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return false, nil
	}
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), "-devops") {
			return true, nil
		}
	}
	return false, nil
}

func isDockerBuildModuleDir(dir string) (bool, error) {
	buildContexts, err := ResolveDockerBuildContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, ErrDockerBuildContextNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(buildContexts) > 0, nil
}

func resolveDockerBuildProjectRoot(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (string, error) {
	if projectRoot := strings.TrimSpace(target.ProjectRoot); projectRoot != "" {
		return filepath.Clean(projectRoot), nil
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return projectRoot, nil
}

func resolveDockerBuildEnvironment(store DockerStore, findProjectRoot ProjectFinderFunc, projectRoot, environment string) (string, error) {
	if environment = strings.TrimSpace(environment); environment != "" {
		return environment, nil
	}

	cleanProjectRoot := filepath.Clean(projectRoot)
	if environment, err := dockerBuildEnvironmentFromTenantConfigs(store, cleanProjectRoot); environment != "" || err != nil {
		return environment, err
	}
	return dockerBuildEnvironmentFromDetectedProject(store, findProjectRoot, cleanProjectRoot)
}

func dockerBuildEnvironmentFromTenantConfigs(store DockerStore, cleanProjectRoot string) (string, error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
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

func dockerBuildEnvironmentFromTenantEnvs(store DockerStore, tenant, cleanProjectRoot string) (string, error) {
	envs, err := store.ListEnvConfigs(tenant)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
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

func dockerBuildEnvironmentFromDetectedProject(store DockerStore, findProjectRoot ProjectFinderFunc, cleanProjectRoot string) (string, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	if filepath.Clean(detectedProjectRoot) != cleanProjectRoot || strings.TrimSpace(tenant) == "" {
		return "", nil
	}

	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(tenantConfig.DefaultEnvironment), nil
}

// ResolveDockerBuildEnvConfig returns the saved EnvConfig a build resolves to,
// or nil when none matches. The build path is otherwise env-agnostic; this is
// the seam for reading per-env build toggles such as DisableBuildScript.
// Best-effort: any store error or a miss yields nil so the build proceeds with
// default behaviour.
func ResolveDockerBuildEnvConfig(store DockerStore, findProjectRoot ProjectFinderFunc, target DockerCommandTarget) *EnvConfig {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return injectedDockerBuildEnvConfig(nil)
	}
	return resolveDockerBuildEnvConfigForProject(store, projectRoot, target.Environment)
}

// resolveDockerBuildEnvConfigForProject is ResolveDockerBuildEnvConfig's core,
// usable by callers that already know the resolved project root and
// environment name directly (e.g. newDockerBuildSpec) without re-deriving them
// through a DockerCommandTarget.
func resolveDockerBuildEnvConfigForProject(store DockerStore, projectRoot, wantEnv string) *EnvConfig {
	cleanRoot := filepath.Clean(projectRoot)

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return injectedDockerBuildEnvConfig(nil)
	}
	wantEnv = strings.TrimSpace(wantEnv)
	for _, tenantConfig := range tenants {
		envs, err := store.ListEnvConfigs(tenantConfig.Name)
		if err != nil {
			continue
		}
		if match := matchDockerBuildEnvConfig(envs, wantEnv, cleanRoot); match != nil {
			return injectedDockerBuildEnvConfig(match)
		}
	}
	return injectedDockerBuildEnvConfig(nil)
}

// injectedDockerBuildEnvConfig lets the values the chart injected win over the
// on-disk copy when the build runs inside a runtime pod.
//
// That copy is a projection, and it is rewritten over the environment's life; a
// build that trusted it resolved the default registry instead of the one the
// environment marks for building, and stopped honouring disablebuildscript, so it
// ran the project's own build script and minted no image version. Both failures
// are silent and neither is recoverable from the build output, so the pod reads
// its own identity from the env the chart set rather than from a file anything
// else may have written since.
func injectedDockerBuildEnvConfig(onDisk *EnvConfig) *EnvConfig {
	injected, ok := ResolveInjectedRuntimeConfig(os.Getenv)
	if !ok {
		return onDisk
	}
	base := EnvConfig{}
	if onDisk != nil {
		base = *onDisk
	}
	merged := overlayInjectedEnvConfig(base, injected.Env)
	return &merged
}

func matchDockerBuildEnvConfig(envs []EnvConfig, wantEnv, cleanRoot string) *EnvConfig {
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
