package projectpaths

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// ResolveProjectComponent selects a components: entry for build/push. An empty
// name picks the lone entry; ambiguity or an unknown name fails listing the
// choices. No components: map returns ok=false so paths: applies.
func ResolveProjectComponent(projectRoot, requested string) (string, configpkg.ProjectPathsConfig, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", configpkg.ProjectPathsConfig{}, false, nil
	}
	config, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return "", configpkg.ProjectPathsConfig{}, false, nil
		}
		return "", configpkg.ProjectPathsConfig{}, false, err
	}

	requested = strings.TrimSpace(requested)
	if requested != "" {
		paths, ok := config.Components[requested]
		if !ok {
			return "", configpkg.ProjectPathsConfig{}, false, fmt.Errorf("component %q not declared in .erun/config.yaml components (declared: %s)", requested, componentNamesOrNone(config.Components))
		}
		return requested, paths, true, nil
	}

	switch len(config.Components) {
	case 0:
		return "", configpkg.ProjectPathsConfig{}, false, nil
	case 1:
		for name, paths := range config.Components {
			return name, paths, true, nil
		}
	}
	return "", configpkg.ProjectPathsConfig{}, false, fmt.Errorf("ambiguous component selection: .erun/config.yaml declares %d components (%s); pass --component <name>", len(config.Components), componentNamesOrNone(config.Components))
}

// DeclaredComponentPaths looks up a components: entry by name; callers fall back
// to whole-project discovery when it is absent.
func DeclaredComponentPaths(projectRoot, componentName string) (configpkg.ProjectPathsConfig, bool, error) {
	componentName = strings.TrimSpace(componentName)
	if strings.TrimSpace(projectRoot) == "" || componentName == "" {
		return configpkg.ProjectPathsConfig{}, false, nil
	}
	config, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return configpkg.ProjectPathsConfig{}, false, nil
		}
		return configpkg.ProjectPathsConfig{}, false, err
	}
	paths, ok := config.Components[componentName]
	return paths, ok, nil
}

func componentNamesOrNone(components map[string]configpkg.ProjectPathsConfig) string {
	if len(components) == 0 {
		return "none"
	}
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
