package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveCurrentLinuxBuildScripts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, version string) ([]scriptSpec, error) {
	contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	scripts := make([]scriptSpec, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.BuildScriptPath) == "" {
			continue
		}
		scripts = append(scripts, newScriptSpec(context.Dir, "./build.sh", version))
	}
	if len(scripts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return scripts, nil
}

func ResolveCurrentLinuxReleaseScripts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget, version string) ([]scriptSpec, error) {
	contexts, err := ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return nil, err
	}

	scripts := make([]scriptSpec, 0, len(contexts))
	for _, context := range contexts {
		if strings.TrimSpace(context.ReleaseScriptPath) == "" {
			continue
		}
		scripts = append(scripts, newScriptSpec(context.Dir, "./release.sh", version))
	}
	if len(scripts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return scripts, nil
}

func ResolveCurrentLinuxPackageContexts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) ([]LinuxPackageContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}

	if context, ok, err := LinuxPackageContextAtDir(buildContext.Dir); err != nil {
		return nil, err
	} else if ok {
		return []LinuxPackageContext{context}, nil
	}

	if contexts, err := ResolveLinuxPackageContextsAtDir(buildContext.Dir); err == nil {
		return contexts, nil
	} else if !errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, err
	}

	linuxDir, ok, err := resolveCurrentDevopsLinuxDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveLinuxPackageContextsAtDir(linuxDir)
	}

	return nil, ErrLinuxPackageBuildNotFound
}

func LinuxPackageContextAtDir(dir string) (LinuxPackageContext, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return LinuxPackageContext{}, false, nil
	}
	if filepath.Base(filepath.Dir(dir)) != "linux" {
		return LinuxPackageContext{}, false, nil
	}

	buildScriptPath, buildFound, err := linuxPackageScriptPath(dir, "build.sh")
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	releaseScriptPath, releaseFound, err := linuxPackageScriptPath(dir, "release.sh")
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	if !buildFound && !releaseFound {
		return LinuxPackageContext{}, false, nil
	}

	return LinuxPackageContext{
		Dir:               dir,
		BuildScriptPath:   buildScriptPath,
		ReleaseScriptPath: releaseScriptPath,
	}, true, nil
}

func ResolveLinuxPackageContextsAtDir(dir string) ([]LinuxPackageContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "linux" {
		return nil, ErrLinuxPackageBuildNotFound
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrLinuxPackageBuildNotFound
		}
		return nil, err
	}

	contexts := make([]LinuxPackageContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		context, ok, err := LinuxPackageContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		contexts = append(contexts, context)
	}
	if len(contexts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return contexts, nil
}

func FindComponentLinuxPackageContext(projectRoot, componentName string) (LinuxPackageContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return LinuxPackageContext{}, false, nil
	}

	matches := make([]LinuxPackageContext, 0, 1)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		context, ok, walkErr := componentLinuxPackageContextCandidate(path, d, componentName, err)
		if ok {
			matches = append(matches, context)
		}
		return walkErr
	})
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	if len(matches) == 0 {
		return LinuxPackageContext{}, false, nil
	}
	if len(matches) > 1 {
		return LinuxPackageContext{}, false, fmt.Errorf("multiple linux package contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func componentLinuxPackageContextCandidate(path string, d os.DirEntry, componentName string, err error) (LinuxPackageContext, bool, error) {
	if err != nil {
		return LinuxPackageContext{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			return LinuxPackageContext{}, false, filepath.SkipDir
		}
		return LinuxPackageContext{}, false, nil
	}
	if d.Name() != "build.sh" && d.Name() != "release.sh" {
		return LinuxPackageContext{}, false, nil
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "linux" {
		return LinuxPackageContext{}, false, nil
	}
	return LinuxPackageContextAtDir(dir)
}

func resolveCurrentDevopsLinuxDir(findProjectRoot ProjectFinderFunc, dir string, target DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	linuxDir := filepath.Join(dir, "linux")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
			return "", false, err
		} else if ok {
			return linuxDir, true, nil
		}
	}

	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsLinuxDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	linuxDir, ok, err := detectedProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return linuxDir, ok, err
	}

	candidates, err := findDevopsLinuxDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops linux directories found under project root")
	}
}

func detectedProjectRootDevopsLinuxDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
		return "", false, nil
	}
	linuxDir := filepath.Join(projectRoot, RuntimeReleaseName(tenant), "linux")
	if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
		return "", false, err
	} else if ok {
		return linuxDir, true, nil
	}
	return "", false, nil
}

func findDevopsLinuxDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		linuxDir := filepath.Join(projectRoot, entry.Name(), "linux")
		ok, err := isLinuxPackageModuleDir(linuxDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, linuxDir)
		}
	}
	return candidates, nil
}

func isLinuxPackageModuleDir(dir string) (bool, error) {
	contexts, err := ResolveLinuxPackageContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, ErrLinuxPackageBuildNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(contexts) > 0, nil
}

func linuxPackageScriptPath(dir, name string) (string, bool, error) {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, nil
	}
	return path, true, nil
}

func newScriptSpec(dir, path, version string) scriptSpec {
	return scriptSpec{
		Dir:  dir,
		Path: path,
		Env:  buildScriptEnv(version),
	}
}
